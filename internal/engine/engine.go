package engine

import (
	"context"
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/internal/provider"
)

// Engine executes a scenario's dependency graph.
type Engine struct {
	registry *provider.Registry
}

func New(registry *provider.Registry) *Engine {
	return &Engine{registry: registry}
}

// Result holds the outputs from executing a scenario.
type Result struct {
	Outputs map[string]cty.Value
}

// Execute runs all nodes in topological order and returns scenario outputs.
func (e *Engine) Execute(ctx context.Context, ps *parser.ParsedScenario, g *graph.Graph) (*Result, error) {
	// resolved maps NodeID string → output name → value.
	resolved := make(map[string]map[string]cty.Value)
	// componentInputs caches resolved inputs per component key so internal
	// actions see them as their var.* namespace.
	componentInputs := make(map[string]map[string]cty.Value)

	for _, nodeID := range g.Order {
		node := g.Nodes[nodeID]

		switch node.Kind {
		case graph.ActionNode:
			vars := ps.Variables
			if node.ComponentKey != "" {
				inputs, err := e.componentInputsFor(node.ComponentKey, ps, resolved, componentInputs)
				if err != nil {
					return nil, fmt.Errorf("resolving inputs for %s: %w", nodeID, err)
				}
				vars = inputs
			}
			outputs, err := e.executeAction(ctx, node, vars, resolved)
			if err != nil {
				return nil, fmt.Errorf("executing %s: %w", nodeID, err)
			}
			resolved[string(nodeID)] = outputs
			fmt.Printf("  ✓ %s\n", nodeID)

		case graph.ComponentNode:
			outputs, err := e.resolveComponentOutputs(node, ps, resolved, componentInputs)
			if err != nil {
				return nil, fmt.Errorf("resolving outputs for %s: %w", nodeID, err)
			}
			resolved[string(nodeID)] = outputs
			fmt.Printf("  ✓ %s\n", nodeID)
		}
	}

	// Evaluate scenario-level outputs.
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
	resolved map[string]map[string]cty.Value,
) (map[string]cty.Value, error) {
	p, err := e.registry.Get(node.ActionType)
	if err != nil {
		return nil, err
	}

	// Build an eval context scoped to what this node can see.
	// For component-scoped actions, we need to remap internal action references
	// and provide component variables.
	evalCtx := e.buildNodeEvalContext(node, vars, resolved)

	return p.Execute(ctx, node.Body, evalCtx)
}

// buildNodeEvalContext creates an EvalContext appropriate for the node.
// Component-scoped actions see `var.*` as the component's input values,
// and `action.*` references are resolved within the component's scope.
func (e *Engine) buildNodeEvalContext(
	node *graph.Node,
	vars map[string]cty.Value,
	resolved map[string]map[string]cty.Value,
) *hcl.EvalContext {
	if node.ComponentKey == "" {
		// Scenario-level action: sees scenario variables and all resolved outputs.
		return buildEvalContext(vars, resolved)
	}

	// Component-scoped action: remap internal action IDs to bare action references.
	// e.g., resolved["component.merchant.base.action.sql.create"] → action.sql.create
	// Caller is responsible for passing component-scoped vars (inputs) here.
	componentPrefix := "component." + node.ComponentKey + ".action."
	scopedResolved := make(map[string]map[string]cty.Value)

	for nodeID, outputs := range resolved {
		if len(nodeID) > len(componentPrefix) && nodeID[:len(componentPrefix)] == componentPrefix {
			bare := "action." + nodeID[len(componentPrefix):]
			scopedResolved[bare] = outputs
		}
	}

	return buildEvalContext(vars, scopedResolved)
}

// componentInputsFor returns the resolved var.* namespace for a component,
// computing it on first use and caching for subsequent internal-action calls.
func (e *Engine) componentInputsFor(
	key string,
	ps *parser.ParsedScenario,
	resolved map[string]map[string]cty.Value,
	cache map[string]map[string]cty.Value,
) (map[string]cty.Value, error) {
	if inputs, ok := cache[key]; ok {
		return inputs, nil
	}
	comp, ok := ps.Components[key]
	if !ok {
		return nil, fmt.Errorf("component %q not found", key)
	}
	inputs, err := e.resolveComponentInputs(comp, ps.Variables, resolved)
	if err != nil {
		return nil, err
	}
	cache[key] = inputs
	return inputs, nil
}

// resolveComponentOutputs evaluates a component's output blocks using
// its internal action results.
func (e *Engine) resolveComponentOutputs(
	node *graph.Node,
	ps *parser.ParsedScenario,
	resolved map[string]map[string]cty.Value,
	inputCache map[string]map[string]cty.Value,
) (map[string]cty.Value, error) {
	comp, ok := ps.Components[node.ComponentKey]
	if !ok {
		return nil, fmt.Errorf("component %q not found", node.ComponentKey)
	}

	// Build an eval context with the component's internal action results.
	componentPrefix := "component." + node.ComponentKey + ".action."
	scopedResolved := make(map[string]map[string]cty.Value)
	for nodeID, outputs := range resolved {
		if len(nodeID) > len(componentPrefix) && nodeID[:len(componentPrefix)] == componentPrefix {
			bare := "action." + nodeID[len(componentPrefix):]
			scopedResolved[bare] = outputs
		}
	}

	compVars, err := e.componentInputsFor(node.ComponentKey, ps, resolved, inputCache)
	if err != nil {
		return nil, err
	}

	evalCtx := buildEvalContext(compVars, scopedResolved)

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

	// Start with component variable defaults.
	for _, v := range comp.Def.Variables {
		if v.Default != nil {
			val, diags := v.Default.Value(&hcl.EvalContext{})
			if diags.HasErrors() {
				return nil, fmt.Errorf("evaluating default for %q: %s", v.Name, diags.Error())
			}
			result[v.Name] = val
		}
	}

	// Override with input values from the scenario.
	if comp.Ref.Inputs != nil {
		// The Inputs body contains an "inputs" attribute with an object value.
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
