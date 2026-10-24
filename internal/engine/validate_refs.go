package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/provider"
)

// ValidateReferences type-checks scenario expressions against the
// declared output shapes of the actions and components they
// reference. It catches typos in `action.X.Y.Z` references, basic
// type mismatches (using a number where a string is required), and
// undeclared variable references — all at plan time, without
// running a single provider call.
//
// The check walks action bodies (scenario-level ones against the
// scenario's namespaces, component-internal ones against their
// component's var namespace and actions), scenario output
// expressions, component input expressions and component outputs.
//
// Coverage caveat: actions whose provider schema declares
// `output { type = any }` (e.g. postgres.sql with its open-ended
// RETURNING shape) are typed as `cty.DynamicPseudoType`, which
// makes any attribute access legal. Typos on those stay runtime
// errors. The fix is for providers to declare concrete output
// shapes where they can; the plan check gets stricter for free
// when they do.
//
// Providers that implement provider.ActionPlanner also check each of
// their actions here, against external state such as a database's
// schema: during `orchard plan` from a snapshot, during `orchard run`
// (after Configure) against the live system, before any action
// executes. A planner can report a concrete output type, which then
// types references to that action's outputs.
//
// Actions are checked in dependency order, inside components as well
// as at scenario level, so each action sees the output types of the
// ones before it.
func (e *Engine) ValidateReferences(runCtx context.Context, ps *parser.ParsedScenario, g *graph.Graph) error {
	e.planTypes = map[graph.NodeID]cty.Type{}
	e.unchecked = map[string]bool{}
	e.hints = nil
	e.planned = nil
	if e.keepPlanned {
		e.planned = map[graph.NodeID]map[string]cty.Value{}
	}

	// Typed contexts are rebuilt only when a planner adds a type.
	var ctx *hcl.EvalContext
	compCtx := map[string]*hcl.EvalContext{}
	stale := true

	for _, nodeID := range g.Order {
		node := g.Nodes[nodeID]
		if node.Kind != graph.ActionNode {
			continue
		}
		if stale {
			ctx = e.buildTypedEvalContext(ps, g)
			compCtx = map[string]*hcl.EvalContext{}
			stale = false
		}
		scope := ctx
		if key := node.ComponentKey; key != "" {
			comp, ok := ps.Components[key]
			if !ok {
				continue
			}
			if compCtx[key] == nil {
				compCtx[key] = e.buildComponentTypedEvalContext(key, comp, g)
			}
			scope = compCtx[key]
		}
		nodeCtx := withInstance(scope, node.Instance)
		if err := checkActionBody(nodeCtx, node); err != nil {
			return err
		}
		t, err := e.planAction(runCtx, nodeCtx, node)
		if err != nil {
			return fmt.Errorf("%s%s: %w", node.ID, formatSourceLoc(node.SourceRange), err)
		}
		// A zero Type means the planner didn't say; treat it as dynamic.
		if t != cty.NilType && t != cty.DynamicPseudoType {
			e.planTypes[node.ID] = t
			stale = true
		}
	}
	if stale {
		ctx = e.buildTypedEvalContext(ps, g)
	}

	// Scenario output expressions.
	for _, out := range ps.Scenario.Outputs {
		if err := checkExpr(ctx, out.Value, fmt.Sprintf("output %q", out.Name)); err != nil {
			return err
		}
	}

	// Component input expressions.
	for key, comp := range ps.Components {
		if comp.Ref.Inputs == nil {
			continue
		}
		attrs, diags := comp.Ref.Inputs.JustAttributes()
		if diags.HasErrors() {
			// Input-block well-formedness is checked elsewhere; only
			// expression-level errors concern this pass.
			continue
		}
		for _, attr := range attrs {
			if attr.Name != "inputs" {
				continue
			}
			if err := checkInputsObject(withInstance(ctx, comp.Instance), attr.Expr, key); err != nil {
				return err
			}
		}
	}

	// Component output expressions. Each component's outputs see
	// their own var namespace (the component's declared variables)
	// and only their own internal actions — the same scoping the
	// engine applies when evaluating these expressions at runtime.
	for key, comp := range ps.Components {
		compCtx := e.buildComponentTypedEvalContext(key, comp, g)
		for _, out := range comp.Def.Outputs {
			loc := fmt.Sprintf("component %q output %q", key, out.Name)
			if err := checkExpr(compCtx, out.Value, loc); err != nil {
				return err
			}
		}
	}

	return nil
}

// planAction hands an action's evaluated attributes to its provider's
// ActionPlanner, if it has one, and returns the output type it
// reports. Attributes that depend on other actions are typed unknowns.
// With SetKeepPlannedAttrs on, every action's attributes are evaluated
// and kept, planner or not. A planner with no schema to check against marks its provider
// unchecked rather than failing.
func (e *Engine) planAction(runCtx context.Context, evalCtx *hcl.EvalContext, node *graph.Node) (cty.Type, error) {
	route, err := e.resolveRoute(node.ActionType, node.ProviderAlias)
	if err != nil {
		return cty.DynamicPseudoType, nil // reported by ValidateGraph
	}
	p, err := e.registry.Get(route.providerAlias)
	if err != nil {
		return cty.DynamicPseudoType, nil
	}
	planner, ok := p.(provider.ActionPlanner)
	if !ok && e.planned == nil {
		return cty.DynamicPseudoType, nil
	}
	// ponytail: impure functions (uuid, now, env) run here too,
	// so kept values can differ from what the run later evaluates.
	attrs, err := evalAttrs(node.Body, route.schema, evalCtx)
	if err != nil {
		return cty.DynamicPseudoType, err
	}
	if e.planned != nil {
		e.planned[node.ID] = attrs
	}
	if !ok {
		return cty.DynamicPseudoType, nil
	}
	plan, err := planner.PlanAction(runCtx, route.actionName, attrs)
	if errors.Is(err, provider.ErrNoSchema) {
		e.unchecked[route.providerAlias] = true
		return cty.DynamicPseudoType, nil
	}
	if err != nil {
		return cty.DynamicPseudoType, err
	}
	loc := string(node.ID) + formatSourceLoc(node.SourceRange)
	for _, h := range plan.Hints {
		e.hints = append(e.hints, loc+": "+h)
	}
	return plan.Outputs, nil
}

// Hints returns the non-fatal hints planners reported during the last
// ValidateReferences, each prefixed with its action and location.
func (e *Engine) Hints() []string { return e.hints }

// UncheckedProviders lists, in order, the providers whose actions the
// last ValidateReferences couldn't check for want of a schema (no
// snapshot during `orchard plan`).
func (e *Engine) UncheckedProviders() []string {
	out := make([]string, 0, len(e.unchecked))
	for alias := range e.unchecked {
		out = append(out, alias)
	}
	sort.Strings(out)
	return out
}

// buildComponentTypedEvalContext builds the typed eval context used
// to check a single component's output expressions at plan time.
// It mirrors the engine's runtime scoping: `var.*` carries the
// component's declared variables (typed by their type constraint,
// DynamicPseudoType when none), and `action.<type>.<name>` exposes
// only the component's own internal actions — the bare namespace,
// matching scopedComponentResolved.
func (e *Engine) buildComponentTypedEvalContext(
	key string, comp *parser.ParsedComponent, g *graph.Graph,
) *hcl.EvalContext {
	variables := map[string]cty.Value{}

	if len(comp.Def.Variables) > 0 {
		varAttrs := make(map[string]cty.Value, len(comp.Def.Variables))
		for _, v := range comp.Def.Variables {
			t, err := parser.ResolveVariableType(v)
			if err != nil {
				t = cty.DynamicPseudoType
			}
			varAttrs[v.Name] = cty.UnknownVal(t)
		}
		variables["var"] = cty.ObjectVal(varAttrs)
	}

	actionsByType := make(map[string]map[string]cty.Value)
	for id, node := range g.Nodes {
		if node.Kind != graph.ActionNode || node.ComponentKey != key {
			continue
		}
		typ, name := parseActionID(string(id))
		if typ == "" {
			continue
		}
		outputType := e.actionOutputType(node)
		if actionsByType[typ] == nil {
			actionsByType[typ] = make(map[string]cty.Value)
		}
		actionsByType[typ][name] = cty.UnknownVal(outputType)
	}
	if v, ok := wrapGrouped(actionsByType); ok {
		variables["action"] = v
	}

	return &hcl.EvalContext{
		Variables: variables,
		Functions: parser.Functions(),
	}
}

// buildTypedEvalContext constructs an eval context where node
// references resolve to typed-but-unknown values. HCL's
// type-checker understands unknown values and propagates them
// through expressions, so downstream operations (attribute access,
// type conversion) still run their usual checks.
func (e *Engine) buildTypedEvalContext(ps *parser.ParsedScenario, g *graph.Graph) *hcl.EvalContext {
	variables := map[string]cty.Value{}

	// var.* carries actual resolved values — we have them at plan
	// time and they're already the right type.
	if len(ps.Variables) > 0 {
		variables["var"] = cty.ObjectVal(ps.Variables)
	}

	// action.<type>.<name> → UnknownVal of the action's declared
	// output type. When the schema doesn't declare one (most
	// common case today), the type is DynamicPseudoType and HCL
	// permits any attribute access.
	actionsByType := make(map[string]map[string]cty.Value)
	for id, node := range g.Nodes {
		if node.Kind != graph.ActionNode || node.ComponentKey != "" {
			continue
		}
		outputType := e.actionOutputType(node)
		typ, name := parseActionID(string(id))
		if typ == "" {
			continue
		}
		if actionsByType[typ] == nil {
			actionsByType[typ] = make(map[string]cty.Value)
		}
		actionsByType[typ][name] = cty.UnknownVal(outputType)
	}
	if v, ok := wrapGrouped(actionsByType); ok {
		variables["action"] = v
	}

	// component.<type>.<name> → typed object whose attributes come
	// from evaluating each declared output expression against the
	// component's own typed scope. This is what makes typos like
	// `component.c.one.idd` fail at plan time: HCL's type checker
	// rejects attribute access on an object that doesn't declare
	// that field.
	//
	// Individual outputs whose expressions don't type-check (a typo
	// inside the component itself) fall back to DynamicPseudoType
	// for that one field; the dedicated component-output pass below
	// still reports the underlying error with full location info.
	componentsByType := make(map[string]map[string]cty.Value)
	for key, comp := range ps.Components {
		typ, name := splitComponentKey(key)
		if typ == "" {
			continue
		}
		if componentsByType[typ] == nil {
			componentsByType[typ] = make(map[string]cty.Value)
		}
		componentsByType[typ][name] = e.componentOutputsObject(key, comp, g)
	}
	if v, ok := wrapGrouped(componentsByType); ok {
		variables["component"] = v
	}

	return &hcl.EvalContext{
		Variables: variables,
		Functions: parser.Functions(),
	}
}

// componentOutputsObject returns a typed cty object value
// representing the outputs of one component instance. Each declared
// output's expression is evaluated against the component's own
// typed eval context; the resulting value (an unknown with concrete
// type) becomes one attribute of the returned object.
//
// When an expression fails to type-check — typically a typo inside
// the component body — the attribute falls back to
// DynamicPseudoType so the scenario-level pass can still complete.
// The component-output pass in ValidateReferences re-evaluates the
// same expression and reports the original diagnostic with location.
func (e *Engine) componentOutputsObject(
	key string, comp *parser.ParsedComponent, g *graph.Graph,
) cty.Value {
	compCtx := e.buildComponentTypedEvalContext(key, comp, g)
	fields := make(map[string]cty.Value, len(comp.Def.Outputs))
	for _, out := range comp.Def.Outputs {
		val, diags := out.Value.Value(compCtx)
		if diags.HasErrors() {
			fields[out.Name] = cty.UnknownVal(cty.DynamicPseudoType)
			continue
		}
		fields[out.Name] = val
	}
	return cty.ObjectVal(fields)
}

// actionOutputType returns the type an ActionPlanner reported for the
// node, or else resolves its route and returns the cty Type of its
// declared output schema. Falls back to
// DynamicPseudoType when the routing is unresolvable (already
// flagged by ValidateGraph) or the schema has no output declared.
func (e *Engine) actionOutputType(node *graph.Node) cty.Type {
	if t, ok := e.planTypes[node.ID]; ok {
		return t
	}
	route, err := e.resolveRoute(node.ActionType, node.ProviderAlias)
	if err != nil {
		return cty.DynamicPseudoType
	}
	if route.schema.Outputs == nil {
		return cty.DynamicPseudoType
	}
	t, err := route.schema.Outputs.CtyType()
	if err != nil {
		return cty.DynamicPseudoType
	}
	return t
}

// checkActionBody walks an action's attribute expressions and
// surfaces any HCL diagnostic as a scenario-level error. Reserved
// attributes (provider, depends_on, timeout) are skipped — their
// values were already validated as string/list literals during
// graph construction.
func checkActionBody(ctx *hcl.EvalContext, node *graph.Node) error {
	if node.Body == nil {
		return nil
	}
	hb, ok := node.Body.(*hclsyntax.Body)
	if !ok {
		return nil
	}
	for name, attr := range hb.Attributes {
		if reservedAttrNamesEngine[name] {
			continue
		}
		if err := checkExpr(ctx, attr.Expr, fmt.Sprintf("%s.%s", node.ID, name)); err != nil {
			return err
		}
	}
	return nil
}

// checkInputsObject handles the RHS of `inputs = { k = v, ... }`
// in a component reference. We care about the VALUE expressions —
// the KEYS were already validated by parser.ValidateComponentInputs
// at load time.
func checkInputsObject(ctx *hcl.EvalContext, expr hcl.Expression, componentKey string) error {
	pairs, diags := hcl.ExprMap(expr)
	if diags.HasErrors() {
		// Non-literal inputs expression. Fall back to whole-expression
		// evaluation so at least top-level references are checked.
		return checkExpr(ctx, expr, fmt.Sprintf("component %q inputs", componentKey))
	}
	for _, p := range pairs {
		name := hcl.ExprAsKeyword(p.Key)
		loc := fmt.Sprintf("component %q inputs.%s", componentKey, name)
		if name == "" {
			loc = fmt.Sprintf("component %q inputs", componentKey)
		}
		if err := checkExpr(ctx, p.Value, loc); err != nil {
			return err
		}
	}
	return nil
}

// checkExpr evaluates expr against ctx and converts any HCL
// diagnostic into an error tagged with location. The typed
// context contains unknown values, so a successful Value() call
// simply means "all references resolved and types line up" —
// runtime will still produce concrete outputs.
func checkExpr(ctx *hcl.EvalContext, expr hcl.Expression, location string) error {
	_, diags := expr.Value(ctx)
	if diags.HasErrors() {
		return fmt.Errorf("%s: %s", location, diags.Error())
	}
	return nil
}

// parseActionID splits a node ID like "action.sql.create" or
// "component.merchant.base.action.sql.create" into (type, name).
// Mirrors planview's helper of the same name; duplicated here so
// engine internals don't pull in the CLI rendering package.
func parseActionID(id string) (kind, name string) {
	rest := id
	if after, ok := strings.CutPrefix(rest, "component."); ok {
		if idx := strings.Index(after, ".action."); idx >= 0 {
			rest = "action." + after[idx+len(".action."):]
		}
	}
	if after, ok := strings.CutPrefix(rest, "action."); ok {
		parts := strings.SplitN(after, ".", 2)
		if len(parts) == 2 {
			return parts[0], parts[1]
		}
	}
	return "", id
}

// splitComponentKey turns "merchant.base" into ("merchant", "base").
func splitComponentKey(key string) (prefix, suffix string) {
	if parts := strings.SplitN(key, ".", 2); len(parts) == 2 {
		return parts[0], parts[1]
	}
	return key, ""
}

// reservedAttrNamesEngine mirrors graph's reserved name set for
// the engine's validation pass. Duplicated (rather than shared)
// because they serve different callers and happen to coincide;
// keeping them separate means the graph's pass and the engine's
// can diverge if needed.
var reservedAttrNamesEngine = map[string]bool{
	"provider":   true,
	"depends_on": true,
	"timeout":    true,
	"count":      true,
	"for_each":   true,
	"retry":      true, // in case we ever see it as an attribute form
}
