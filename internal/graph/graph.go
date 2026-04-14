package graph

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/pthm/orchard/internal/parser"
)

// NodeKind distinguishes action nodes from component nodes.
type NodeKind int

const (
	ActionNode NodeKind = iota
	ComponentNode
)

// NodeID uniquely identifies a node in the execution graph.
type NodeID string

// Node is a unit of work in the execution graph.
type Node struct {
	ID           NodeID
	Kind         NodeKind
	ActionType   string // "sql", "cmd", "http", etc.
	Body         hcl.Body
	Dependencies []NodeID
	// ComponentKey links component-scoped actions back to their parent.
	// Empty for scenario-level actions.
	ComponentKey string
}

// Graph holds the dependency graph built from a parsed scenario.
type Graph struct {
	Nodes map[NodeID]*Node
	Order []NodeID // topological execution order
}

// Build constructs a dependency graph from a parsed scenario.
func Build(ps *parser.ParsedScenario) (*Graph, error) {
	nodes := make(map[NodeID]*Node)

	// Register scenario-level actions.
	for _, action := range ps.Scenario.Actions {
		id := NodeID(fmt.Sprintf("action.%s.%s", action.Type, action.Name))
		nodes[id] = &Node{
			ID:         id,
			Kind:       ActionNode,
			ActionType: action.Type,
			Body:       action.Remain,
		}
	}

	// Register component nodes and their internal actions.
	for key, comp := range ps.Components {
		compID := NodeID("component." + key)

		// Register internal actions for this component.
		internalActionIDs := make([]NodeID, 0, len(comp.Def.Actions))
		for _, action := range comp.Def.Actions {
			actionID := NodeID(fmt.Sprintf("component.%s.action.%s.%s", key, action.Type, action.Name))
			nodes[actionID] = &Node{
				ID:           actionID,
				Kind:         ActionNode,
				ActionType:   action.Type,
				Body:         action.Remain,
				ComponentKey: key,
			}
			internalActionIDs = append(internalActionIDs, actionID)
		}

		// The component node itself depends on all its internal actions.
		// It acts as a "join" node that exposes outputs.
		nodes[compID] = &Node{
			ID:           compID,
			Kind:         ComponentNode,
			Dependencies: internalActionIDs,
			ComponentKey: key,
		}
	}

	// Extract dependencies from expressions.
	for _, node := range nodes {
		if node.Body == nil {
			continue
		}
		deps, err := extractDependencies(node.Body, node.ComponentKey)
		if err != nil {
			return nil, fmt.Errorf("extracting dependencies for %s: %w", node.ID, err)
		}
		node.Dependencies = append(node.Dependencies, deps...)
	}

	// Extract dependencies from component input expressions and
	// attach them both to the component node (so outputs wait on
	// their inputs) and to every internal action (so the component's
	// inputs are resolvable before any internal action runs — the
	// engine uses input values as the component-scoped var namespace).
	for key, comp := range ps.Components {
		if comp.Ref.Inputs == nil {
			continue
		}
		deps, err := extractDependencies(comp.Ref.Inputs, "")
		if err != nil {
			return nil, fmt.Errorf("extracting input dependencies for component %s: %w", key, err)
		}
		compID := NodeID("component." + key)
		nodes[compID].Dependencies = append(nodes[compID].Dependencies, deps...)

		for _, action := range comp.Def.Actions {
			actionID := NodeID(fmt.Sprintf("component.%s.action.%s.%s", key, action.Type, action.Name))
			if n, ok := nodes[actionID]; ok {
				n.Dependencies = append(n.Dependencies, deps...)
			}
		}
	}

	// Also extract dependencies from scenario-level output expressions.
	for _, output := range ps.Scenario.Outputs {
		deps := extractExprDependencies(output.Value, "")
		// Output deps don't create new nodes; they create ordering constraints.
		// For now, outputs are evaluated after all actions complete.
		_ = deps
	}

	// Topological sort.
	order, err := topoSort(nodes)
	if err != nil {
		return nil, err
	}

	return &Graph{Nodes: nodes, Order: order}, nil
}

// extractDependencies inspects all expressions in a body for variable references
// and returns the corresponding node IDs as dependencies.
func extractDependencies(body hcl.Body, componentKey string) ([]NodeID, error) {
	attrs, diags := body.JustAttributes()
	if diags.HasErrors() {
		return nil, fmt.Errorf("reading attributes: %s", diags.Error())
	}

	seen := make(map[NodeID]bool)
	var deps []NodeID

	for _, attr := range attrs {
		for _, dep := range extractExprDependencies(attr.Expr, componentKey) {
			if !seen[dep] {
				seen[dep] = true
				deps = append(deps, dep)
			}
		}
	}

	return deps, nil
}

// extractExprDependencies returns node IDs referenced by an expression.
func extractExprDependencies(expr hcl.Expression, componentKey string) []NodeID {
	var deps []NodeID
	for _, traversal := range expr.Variables() {
		if dep, ok := traversalToNodeID(traversal, componentKey); ok {
			deps = append(deps, dep)
		}
	}
	return deps
}

// traversalToNodeID converts an HCL traversal to a dependency NodeID.
// For "action.sql.create_merchant.id" → NodeID("action.sql.create_merchant")
// For "component.merchant.base.id" → NodeID("component.merchant.base")
// For "var.name" → no dependency (variables are pre-resolved)
func traversalToNodeID(traversal hcl.Traversal, componentKey string) (NodeID, bool) {
	if len(traversal) < 3 {
		return "", false
	}

	root := traversal.RootName()
	switch root {
	case "action":
		// action.<type>.<name>[.<output>]
		if len(traversal) < 3 {
			return "", false
		}
		actionType, ok := traversalAttr(traversal[1])
		if !ok {
			return "", false
		}
		actionName, ok := traversalAttr(traversal[2])
		if !ok {
			return "", false
		}
		// If inside a component, internal action references are scoped.
		if componentKey != "" {
			return NodeID(fmt.Sprintf("component.%s.action.%s.%s", componentKey, actionType, actionName)), true
		}
		return NodeID(fmt.Sprintf("action.%s.%s", actionType, actionName)), true

	case "component":
		// component.<type>.<name>[.<output>]
		if len(traversal) < 3 {
			return "", false
		}
		compType, ok := traversalAttr(traversal[1])
		if !ok {
			return "", false
		}
		compName, ok := traversalAttr(traversal[2])
		if !ok {
			return "", false
		}
		return NodeID(fmt.Sprintf("component.%s.%s", compType, compName)), true

	case "var":
		// Variables are pre-resolved, no graph dependency.
		return "", false
	}

	return "", false
}

func traversalAttr(t hcl.Traverser) (string, bool) {
	switch v := t.(type) {
	case hcl.TraverseAttr:
		return v.Name, true
	default:
		return "", false
	}
}
