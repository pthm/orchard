package engine

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
)

// ValidateReferences type-checks scenario expressions against the
// declared output shapes of the actions and components they
// reference. It catches typos in `action.X.Y.Z` references, basic
// type mismatches (using a number where a string is required), and
// undeclared variable references — all at plan time, without
// running a single provider call.
//
// The check walks three places: scenario-level action body
// attributes, scenario output expressions, and component input
// expressions. Component-internal action bodies evaluate against
// the component's local var namespace and are deferred to a later
// pass — those references surface at execute time today.
//
// Coverage caveat: actions whose provider schema declares
// `output { type = any }` (e.g. postgres.sql with its open-ended
// RETURNING shape) are typed as `cty.DynamicPseudoType`, which
// makes any attribute access legal. Typos on those stay runtime
// errors. The fix is for providers to declare concrete output
// shapes where they can; the plan check gets stricter for free
// when they do.
func (e *Engine) ValidateReferences(ps *parser.ParsedScenario, g *graph.Graph) error {
	ctx := e.buildTypedEvalContext(ps, g)

	// Scenario-level action bodies.
	for _, nodeID := range g.Order {
		node := g.Nodes[nodeID]
		if node.Kind != graph.ActionNode || node.ComponentKey != "" {
			continue
		}
		if err := checkActionBody(ctx, node); err != nil {
			return err
		}
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
			if err := checkInputsObject(ctx, attr.Expr, key); err != nil {
				return err
			}
		}
	}

	return nil
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
	if len(actionsByType) > 0 {
		objs := make(map[string]cty.Value, len(actionsByType))
		for typ, byName := range actionsByType {
			objs[typ] = cty.ObjectVal(byName)
		}
		variables["action"] = cty.ObjectVal(objs)
	}

	// component.<type>.<name> → DynamicPseudoType unknown for v1.
	// Deriving concrete component output types requires recursively
	// typing the component's own output expressions — deferred.
	componentsByType := make(map[string]map[string]cty.Value)
	for key := range ps.Components {
		typ, name := splitComponentKey(key)
		if typ == "" {
			continue
		}
		if componentsByType[typ] == nil {
			componentsByType[typ] = make(map[string]cty.Value)
		}
		componentsByType[typ][name] = cty.UnknownVal(cty.DynamicPseudoType)
	}
	if len(componentsByType) > 0 {
		objs := make(map[string]cty.Value, len(componentsByType))
		for typ, byName := range componentsByType {
			objs[typ] = cty.ObjectVal(byName)
		}
		variables["component"] = cty.ObjectVal(objs)
	}

	return &hcl.EvalContext{Variables: variables}
}

// actionOutputType resolves a node's route and returns the cty
// Type of its declared output schema. Falls back to
// DynamicPseudoType when the routing is unresolvable (already
// flagged by ValidateGraph) or the schema has no output declared.
func (e *Engine) actionOutputType(node *graph.Node) cty.Type {
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
func parseActionID(id string) (string, string) {
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
func splitComponentKey(key string) (string, string) {
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
	"retry":      true, // in case we ever see it as an attribute form
}
