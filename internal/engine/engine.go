package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

// Engine executes a scenario's dependency graph.
type Engine struct {
	registry *provider.Registry
	// routes maps a scenario-level action type (the first label in
	// `action "TYPE" "name" {}`) to the set of registered providers
	// that offer it. More than one entry is allowed — scenarios
	// disambiguate at the action level with `provider = "<alias>"`.
	// Built once at construction time from registered providers'
	// schemas.
	routes   map[string][]actionRoute
	reporter Reporter
}

// Reporter receives observability events from the engine's
// execution loop. The engine has no opinion on how events are
// surfaced — CLI spinners, structured logs, test-silence, or a
// future TUI all implement this interface. Scenarios can take
// minutes to run; making this hook pluggable keeps the engine
// independent of presentation.
type Reporter interface {
	NodeCompleted(nodeID string)
}

// NopReporter discards all events. Engine uses it by default so
// callers that do not care about progress pay zero cost.
type NopReporter struct{}

// NodeCompleted implements Reporter.
func (NopReporter) NodeCompleted(string) {}

// actionRoute records one (provider, action) candidate for a
// given scenario action type. When multiple candidates exist for
// an action type, the scenario must pick one explicitly via
// `provider = "<alias>"` on the action body.
type actionRoute struct {
	providerAlias string                // local name in the registry
	actionName    string                // schema's action name (usually == action type)
	schema        protocol.ActionSchema // used by evalAttrs and validate
}

// Result holds the outputs from executing a scenario.
type Result struct {
	Outputs map[string]cty.Value
}

// New constructs an Engine over the given provider registry. It also
// builds the action-type routing table, failing if two providers
// declare the same action name — that ambiguity must be resolved by
// the caller (via an explicit provider choice in the scenario,
// coming in a later milestone) before execution can proceed.
//
// The engine installs a NopReporter by default. Call SetReporter to
// receive progress events.
func New(registry *provider.Registry) (*Engine, error) {
	routes, err := buildRoutes(registry)
	if err != nil {
		return nil, err
	}
	return &Engine{registry: registry, routes: routes, reporter: NopReporter{}}, nil
}

// SetReporter installs a Reporter for execution events. Call before
// Execute; there is no synchronization around the reporter field.
func (e *Engine) SetReporter(r Reporter) {
	if r == nil {
		r = NopReporter{}
	}
	e.reporter = r
}

func buildRoutes(registry *provider.Registry) (map[string][]actionRoute, error) {
	routes := make(map[string][]actionRoute)
	for _, name := range registry.Names() {
		p, err := registry.Get(name)
		if err != nil {
			return nil, err
		}
		schema := p.Schema()
		for actionName, actionSchema := range schema.Actions {
			routes[actionName] = append(routes[actionName], actionRoute{
				providerAlias: name,
				actionName:    actionName,
				schema:        actionSchema,
			})
		}
	}
	return routes, nil
}

// resolveRoute picks the candidate for the given scenario action
// type. When the scenario supplied a `provider = "<alias>"`
// attribute, that alias must match exactly one candidate. When no
// alias is supplied, there must be exactly one candidate; any
// other situation is an authoring error surfaced here.
func (e *Engine) resolveRoute(actionType, providerAlias string) (actionRoute, error) {
	candidates := e.routes[actionType]
	if len(candidates) == 0 {
		return actionRoute{}, fmt.Errorf("no provider registered for action type %q", actionType)
	}
	if providerAlias != "" {
		for _, c := range candidates {
			if c.providerAlias == providerAlias {
				return c, nil
			}
		}
		aliases := make([]string, len(candidates))
		for i, c := range candidates {
			aliases[i] = c.providerAlias
		}
		sort.Strings(aliases)
		return actionRoute{}, fmt.Errorf(
			"provider %q does not expose action %q (providers offering it: %s)",
			providerAlias, actionType, strings.Join(aliases, ", "))
	}
	if len(candidates) > 1 {
		aliases := make([]string, len(candidates))
		for i, c := range candidates {
			aliases[i] = c.providerAlias
		}
		sort.Strings(aliases)
		return actionRoute{}, fmt.Errorf(
			"action type %q is offered by multiple providers (%s); "+
				"add `provider = \"<alias>\"` to the action body to disambiguate",
			actionType, strings.Join(aliases, ", "))
	}
	return candidates[0], nil
}

// Execute runs all nodes in topological order and returns scenario outputs.
func (e *Engine) Execute(ctx context.Context, ps *parser.ParsedScenario, g *graph.Graph) (*Result, error) {
	resolved := make(map[string]map[string]cty.Value)
	// compVars holds each component's resolved inputs, indexed by
	// component key. Populated lazily on first access so components
	// with input expressions that reference upstream action outputs
	// still resolve against the latest `resolved` map — the graph
	// already ensures those upstreams have run by the time we need
	// the inputs.
	compVars := make(map[string]map[string]cty.Value)

	for _, nodeID := range g.Order {
		node := g.Nodes[nodeID]

		switch node.Kind {
		case graph.ActionNode:
			if node.ComponentKey != "" {
				if _, ok := compVars[node.ComponentKey]; !ok {
					vars, err := e.resolveComponentInputs(ps.Components[node.ComponentKey], ps.Variables, resolved)
					if err != nil {
						return nil, fmt.Errorf("resolving inputs for component %q: %w", node.ComponentKey, err)
					}
					compVars[node.ComponentKey] = vars
				}
			}
			outputs, err := e.executeAction(ctx, node, ps.Variables, compVars, resolved)
			if err != nil {
				return nil, fmt.Errorf("executing %s: %w", nodeID, err)
			}
			resolved[string(nodeID)] = outputs
			e.reporter.NodeCompleted(string(nodeID))

		case graph.ComponentNode:
			outputs, err := e.resolveComponentOutputs(node, ps, compVars, resolved)
			if err != nil {
				return nil, fmt.Errorf("resolving outputs for %s: %w", nodeID, err)
			}
			resolved[string(nodeID)] = outputs
			e.reporter.NodeCompleted(string(nodeID))
		}
	}

	scenarioOutputs := make(map[string]cty.Value)
	evalCtx := buildEvalContext(ps.Variables, resolved)

	for _, output := range ps.Scenario.Outputs {
		val, diags := output.Value.Value(evalCtx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("evaluating output %q: %s", output.Name, diags.Error())
		}
		scenarioOutputs[output.Name] = val
	}

	return &Result{Outputs: scenarioOutputs}, nil
}

func (e *Engine) executeAction(
	ctx context.Context,
	node *graph.Node,
	vars map[string]cty.Value,
	compVars map[string]map[string]cty.Value,
	resolved map[string]map[string]cty.Value,
) (map[string]cty.Value, error) {
	route, err := e.resolveRoute(node.ActionType, node.ProviderAlias)
	if err != nil {
		return nil, err
	}
	p, err := e.registry.Get(route.providerAlias)
	if err != nil {
		return nil, err
	}

	evalCtx := e.buildNodeEvalContext(node, vars, compVars, resolved)

	attrs, err := evalAttrs(node.Body, route.schema, evalCtx)
	if err != nil {
		return nil, err
	}

	return p.Execute(ctx, route.actionName, attrs)
}

// buildNodeEvalContext creates an EvalContext appropriate for the node.
// Component-scoped actions see `var.*` as the component's resolved
// inputs (with defaults applied from the component's own variable
// declarations), and `action.*` references are rewritten to expose
// only the internal actions belonging to the same component.
func (e *Engine) buildNodeEvalContext(
	node *graph.Node,
	vars map[string]cty.Value,
	compVars map[string]map[string]cty.Value,
	resolved map[string]map[string]cty.Value,
) *hcl.EvalContext {
	if node.ComponentKey == "" {
		return buildEvalContext(vars, resolved)
	}

	componentPrefix := "component." + node.ComponentKey + ".action."
	scopedResolved := make(map[string]map[string]cty.Value)
	for nodeID, outputs := range resolved {
		if len(nodeID) > len(componentPrefix) && nodeID[:len(componentPrefix)] == componentPrefix {
			bare := "action." + nodeID[len(componentPrefix):]
			scopedResolved[bare] = outputs
		}
	}

	scopedVars := compVars[node.ComponentKey]
	if scopedVars == nil {
		// Component with no inputs block and no declared variables.
		scopedVars = map[string]cty.Value{}
	}
	return buildEvalContext(scopedVars, scopedResolved)
}

// resolveComponentOutputs evaluates a component's output blocks using
// its internal action results and the component-scoped var namespace.
//
// compVars is expected to already contain the component's resolved
// inputs — populated by Execute before this node runs, so that both
// internal actions and output blocks see the same var context.
func (e *Engine) resolveComponentOutputs(
	node *graph.Node,
	ps *parser.ParsedScenario,
	compVars map[string]map[string]cty.Value,
	resolved map[string]map[string]cty.Value,
) (map[string]cty.Value, error) {
	comp, ok := ps.Components[node.ComponentKey]
	if !ok {
		return nil, fmt.Errorf("component %q not found", node.ComponentKey)
	}

	componentPrefix := "component." + node.ComponentKey + ".action."
	scopedResolved := make(map[string]map[string]cty.Value)
	for nodeID, outputs := range resolved {
		if len(nodeID) > len(componentPrefix) && nodeID[:len(componentPrefix)] == componentPrefix {
			bare := "action." + nodeID[len(componentPrefix):]
			scopedResolved[bare] = outputs
		}
	}

	vars, ok := compVars[node.ComponentKey]
	if !ok {
		// Component with neither inputs nor internal actions: nothing
		// to resolve, but we still need an empty var namespace so
		// output expressions referencing declared component defaults
		// evaluate cleanly.
		v, err := e.resolveComponentInputs(comp, ps.Variables, resolved)
		if err != nil {
			return nil, err
		}
		vars = v
	}

	evalCtx := buildEvalContext(vars, scopedResolved)

	outputs := make(map[string]cty.Value)
	for _, output := range comp.Def.Outputs {
		val, diags := output.Value.Value(evalCtx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("evaluating component output %q: %s", output.Name, diags.Error())
		}
		outputs[output.Name] = val
	}

	return outputs, nil
}

// resolveComponentInputs evaluates the inputs block of a component reference
// in the scenario's scope, returning values to use as the component's var.* namespace.
//
// Validates three contract properties of the component:
//   - every input key matches a declared variable (no stray inputs);
//   - every required (no-default) declared variable has an input or default;
//   - every input value coerces to the declared variable's type.
//
// These checks turn silent "the input was ignored" / "the type drifted"
// bugs into authoring errors raised before the component's actions run.
func (e *Engine) resolveComponentInputs(
	comp *parser.ParsedComponent,
	scenarioVars map[string]cty.Value,
	resolved map[string]map[string]cty.Value,
) (map[string]cty.Value, error) {
	declsByName := make(map[string]*parser.VariableBlock, len(comp.Def.Variables))
	for _, v := range comp.Def.Variables {
		declsByName[v.Name] = v
	}
	compKey := comp.Ref.Type + "." + comp.Ref.Name

	result := make(map[string]cty.Value)

	// Apply coerced defaults first so inputs can override.
	for _, v := range comp.Def.Variables {
		declaredType, err := parser.ResolveVariableType(v)
		if err != nil {
			return nil, fmt.Errorf("component %q variable %q: %w", compKey, v.Name, err)
		}
		defaultExpr, err := parser.VariableDefault(v)
		if err != nil {
			return nil, fmt.Errorf("component %q variable %q: %w", compKey, v.Name, err)
		}
		if defaultExpr == nil {
			continue
		}
		val, diags := defaultExpr.Value(&hcl.EvalContext{})
		if diags.HasErrors() {
			return nil, fmt.Errorf("component %q variable %q default: %s",
				compKey, v.Name, diags.Error())
		}
		coerced, err := coerceVar(val, declaredType)
		if err != nil {
			return nil, fmt.Errorf("component %q variable %q default: %w", compKey, v.Name, err)
		}
		result[v.Name] = coerced
	}

	if comp.Ref.Inputs != nil {
		type inputsWrapper struct {
			Inputs map[string]cty.Value `hcl:"inputs,optional"`
		}
		scenarioCtx := buildEvalContext(scenarioVars, resolved)
		var wrapper inputsWrapper
		diags := gohcl.DecodeBody(comp.Ref.Inputs, scenarioCtx, &wrapper)
		if diags.HasErrors() {
			return nil, fmt.Errorf("component %q inputs: %s", compKey, diags.Error())
		}
		for name, val := range wrapper.Inputs {
			decl, ok := declsByName[name]
			if !ok {
				return nil, fmt.Errorf(
					"component %q: input %q is not a declared variable on component %q",
					compKey, name, comp.Def.Name)
			}
			declaredType, err := parser.ResolveVariableType(decl)
			if err != nil {
				return nil, fmt.Errorf("component %q variable %q: %w", compKey, name, err)
			}
			coerced, err := coerceVar(val, declaredType)
			if err != nil {
				return nil, fmt.Errorf("component %q input %q: %w", compKey, name, err)
			}
			result[name] = coerced
		}
	}

	// Every declared variable must have either a default or an input.
	for name, decl := range declsByName {
		if _, present := result[name]; present {
			continue
		}
		defaultExpr, err := parser.VariableDefault(decl)
		if err != nil {
			return nil, err
		}
		if defaultExpr == nil {
			return nil, fmt.Errorf(
				"component %q: required variable %q has no default and no input",
				compKey, name)
		}
	}

	return result, nil
}

// coerceVar is the engine-side twin of the parser's coerceToType.
// Kept local rather than exporting from parser so the engine has
// a single concept ("coerce against declared type, dynamic = accept
// as-is") without worrying about cyclic imports.
func coerceVar(val cty.Value, declared cty.Type) (cty.Value, error) {
	if declared == cty.DynamicPseudoType {
		return val, nil
	}
	coerced, err := convert.Convert(val, declared)
	if err != nil {
		return cty.NilVal, fmt.Errorf("type mismatch: %w", err)
	}
	return coerced, nil
}
