package engine

import (
	"context"
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

// Engine executes a scenario's dependency graph.
type Engine struct {
	registry *provider.Registry
	// routes maps a scenario-level action type (the first label in
	// `action "TYPE" "name" {}`) to the provider that owns it. Built
	// once at construction time from registered providers' schemas.
	routes map[string]actionRoute
}

// actionRoute records which registered provider handles a given
// scenario action type, and which of that provider's action names
// it maps to.
type actionRoute struct {
	providerName string
	actionName   string
	schema       protocol.ActionSchema
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
func New(registry *provider.Registry) (*Engine, error) {
	routes, err := buildRoutes(registry)
	if err != nil {
		return nil, err
	}
	return &Engine{registry: registry, routes: routes}, nil
}

func buildRoutes(registry *provider.Registry) (map[string]actionRoute, error) {
	routes := make(map[string]actionRoute)
	for _, name := range registry.Names() {
		p, err := registry.Get(name)
		if err != nil {
			return nil, err
		}
		schema := p.Schema()
		for actionName, actionSchema := range schema.Actions {
			if existing, dup := routes[actionName]; dup {
				return nil, fmt.Errorf(
					"action type %q is exposed by both provider %q and %q; "+
						"only one provider per action type is supported today",
					actionName, existing.providerName, name)
			}
			routes[actionName] = actionRoute{
				providerName: name,
				actionName:   actionName,
				schema:       actionSchema,
			}
		}
	}
	return routes, nil
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
			fmt.Printf("  ✓ %s\n", nodeID)

		case graph.ComponentNode:
			outputs, err := e.resolveComponentOutputs(node, ps, compVars, resolved)
			if err != nil {
				return nil, fmt.Errorf("resolving outputs for %s: %w", nodeID, err)
			}
			resolved[string(nodeID)] = outputs
			fmt.Printf("  ✓ %s\n", nodeID)
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
	route, ok := e.routes[node.ActionType]
	if !ok {
		return nil, fmt.Errorf("no provider registered for action type %q", node.ActionType)
	}
	p, err := e.registry.Get(route.providerName)
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
func (e *Engine) resolveComponentInputs(
	comp *parser.ParsedComponent,
	scenarioVars map[string]cty.Value,
	resolved map[string]map[string]cty.Value,
) (map[string]cty.Value, error) {
	result := make(map[string]cty.Value)

	for _, v := range comp.Def.Variables {
		if v.Default != nil {
			val, diags := v.Default.Value(&hcl.EvalContext{})
			if diags.HasErrors() {
				return nil, fmt.Errorf("evaluating default for %q: %s", v.Name, diags.Error())
			}
			result[v.Name] = val
		}
	}

	if comp.Ref.Inputs != nil {
		type inputsWrapper struct {
			Inputs map[string]cty.Value `hcl:"inputs,optional"`
		}
		scenarioCtx := buildEvalContext(scenarioVars, resolved)
		var wrapper inputsWrapper
		diags := gohcl.DecodeBody(comp.Ref.Inputs, scenarioCtx, &wrapper)
		if diags.HasErrors() {
			return nil, fmt.Errorf("decoding component inputs: %s", diags.Error())
		}
		for k, v := range wrapper.Inputs {
			result[k] = v
		}
	}

	return result, nil
}
