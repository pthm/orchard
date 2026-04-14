package graph

import (
	"fmt"
	"strings"
	"time"

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
	// ProviderAlias is the scenario-local provider name picked via the
	// `provider = "<alias>"` reserved action attribute. Empty when
	// the scenario does not specify one; the engine's routing then
	// requires exactly one candidate to be unambiguous.
	ProviderAlias string
	// Timeout bounds the action's execute call. Zero means no
	// engine-imposed deadline (the caller's ctx still applies).
	// Set via `timeout = "30s"` in the action body.
	Timeout time.Duration
}

// Graph holds the dependency graph built from a parsed scenario.
type Graph struct {
	Nodes map[NodeID]*Node
	Order []NodeID // topological execution order
}

// Build constructs a dependency graph from a parsed scenario.
func Build(ps *parser.ParsedScenario) (*Graph, error) {
	nodes := make(map[NodeID]*Node)

	addNode := func(n *Node) error {
		if existing, dup := nodes[n.ID]; dup {
			// Parser-level duplicate detection should catch this before
			// we get here. Retained as a defence-in-depth guard so a
			// future refactor cannot silently overwrite nodes.
			return fmt.Errorf("graph: duplicate node ID %q (kind %v vs %v)",
				n.ID, existing.Kind, n.Kind)
		}
		nodes[n.ID] = n
		return nil
	}

	// Register scenario-level actions.
	for _, action := range ps.Scenario.Actions {
		id := NodeID(fmt.Sprintf("action.%s.%s", action.Type, action.Name))
		reserved, body, err := extractReservedActionAttrs(action.Remain, "")
		if err != nil {
			return nil, fmt.Errorf("action %s: %w", id, err)
		}
		if err := addNode(&Node{
			ID:            id,
			Kind:          ActionNode,
			ActionType:    action.Type,
			Body:          body,
			ProviderAlias: reserved.ProviderAlias,
			Dependencies:  reserved.DependsOn,
			Timeout:       reserved.Timeout,
		}); err != nil {
			return nil, err
		}
	}

	// Register component nodes and their internal actions.
	for key, comp := range ps.Components {
		compID := NodeID("component." + key)

		// Register internal actions for this component.
		internalActionIDs := make([]NodeID, 0, len(comp.Def.Actions))
		for _, action := range comp.Def.Actions {
			actionID := NodeID(fmt.Sprintf("component.%s.action.%s.%s", key, action.Type, action.Name))
			reserved, body, err := extractReservedActionAttrs(action.Remain, key)
			if err != nil {
				return nil, fmt.Errorf("action %s: %w", actionID, err)
			}
			if err := addNode(&Node{
				ID:            actionID,
				Kind:          ActionNode,
				ActionType:    action.Type,
				Body:          body,
				ComponentKey:  key,
				ProviderAlias: reserved.ProviderAlias,
				Dependencies:  reserved.DependsOn,
				Timeout:       reserved.Timeout,
			}); err != nil {
				return nil, err
			}
			internalActionIDs = append(internalActionIDs, actionID)
		}

		// The component node itself depends on all its internal actions.
		// It acts as a "join" node that exposes outputs.
		if err := addNode(&Node{
			ID:           compID,
			Kind:         ComponentNode,
			Dependencies: internalActionIDs,
			ComponentKey: key,
		}); err != nil {
			return nil, err
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

	// Validate scenario-level output expressions. Outputs don't
	// become graph nodes (they're evaluated after every action
	// completes), but their references must still point at real
	// nodes — otherwise `output "x" { value = action.sql.missing.id }`
	// silently passes plan and blows up at runtime.
	for _, output := range ps.Scenario.Outputs {
		deps := extractExprDependencies(output.Value, "")
		for _, dep := range deps {
			if _, ok := nodes[dep]; !ok {
				return nil, fmt.Errorf(
					"output %q references unknown node %q", output.Name, dep)
			}
		}
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

	switch traversal.RootName() {
	case "action":
		// action.<type>.<name>[.<output>]
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
		compType, ok := traversalAttr(traversal[1])
		if !ok {
			return "", false
		}
		compName, ok := traversalAttr(traversal[2])
		if !ok {
			return "", false
		}
		return NodeID(fmt.Sprintf("component.%s.%s", compType, compName)), true
	}

	// var.* is pre-resolved, and anything else is not a graph dependency.
	return "", false
}

func traversalAttr(t hcl.Traverser) (string, bool) {
	if v, ok := t.(hcl.TraverseAttr); ok {
		return v.Name, true
	}
	return "", false
}

// reservedActionSchema is the body-schema used to pull off orchard-
// level attributes from an action body before handing the leftover
// to a provider's own schema validation. Retry controls will join
// it in the rest of Milestone 3.
var reservedActionSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "provider"},
		{Name: "depends_on"},
		{Name: "timeout"},
	},
}

// reservedActionAttrs bundles the attributes orchard owns on an
// action body before schema validation kicks in. Every field is
// optional; callers check for zero values.
type reservedActionAttrs struct {
	// ProviderAlias is the scenario-local provider name picked by
	// a `provider = "<alias>"` attribute. Empty means "let the
	// engine route automatically" (which works when exactly one
	// registered provider offers the action type).
	ProviderAlias string

	// DependsOn lists extra graph dependencies declared explicitly
	// via `depends_on = [action.X.Y, component.A.B]`. Data
	// dependencies are already inferred from expression references
	// in the body, so depends_on is for side-effect ordering only
	// — the common case is "run after this other thing, even
	// though I don't read any of its outputs".
	DependsOn []NodeID

	// Timeout is the deadline the engine wraps around the action's
	// Execute call. Zero means "no timeout; provider controls its
	// own deadlines" (most providers use the caller's ctx plus
	// their own library defaults). The scenario writes Go-style
	// duration strings: `timeout = "30s"`, `timeout = "2m"`.
	Timeout time.Duration
}

// extractReservedActionAttrs pulls orchard-reserved attributes off
// an action body and returns a leftover body that no longer
// contains them, plus the structured attributes. Providers' own
// schemas never see the reserved names, so their strict attribute
// validation works without knowing about orchard internals.
//
// componentKey is "" for scenario-level actions and the component's
// instance key (e.g. "merchant.base") for actions declared inside
// a component. The scoping is used when translating depends_on
// traversals to NodeIDs — inside a component, `action.sql.foo`
// refers to the component's internal action of that name.
func extractReservedActionAttrs(body hcl.Body, componentKey string) (reservedActionAttrs, hcl.Body, error) {
	var out reservedActionAttrs
	if body == nil {
		return out, nil, nil
	}
	content, remain, diags := body.PartialContent(reservedActionSchema)
	if diags.HasErrors() {
		return out, nil, fmt.Errorf("reading reserved attributes: %s", diags.Error())
	}

	if attr, ok := content.Attributes["provider"]; ok {
		val, diags := attr.Expr.Value(nil)
		if diags.HasErrors() {
			return out, nil, fmt.Errorf(`provider = ...: %s`, diags.Error())
		}
		if val.Type().FriendlyName() != "string" {
			return out, nil, fmt.Errorf(`provider must be a string literal, got %s`, val.Type().FriendlyName())
		}
		out.ProviderAlias = val.AsString()
	}

	if attr, ok := content.Attributes["depends_on"]; ok {
		deps, err := extractDependsOnRefs(attr.Expr, componentKey)
		if err != nil {
			return out, nil, fmt.Errorf("depends_on: %w", err)
		}
		out.DependsOn = deps
	}

	if attr, ok := content.Attributes["timeout"]; ok {
		val, diags := attr.Expr.Value(nil)
		if diags.HasErrors() {
			return out, nil, fmt.Errorf(`timeout = ...: %s`, diags.Error())
		}
		if val.Type().FriendlyName() != "string" {
			return out, nil, fmt.Errorf(`timeout must be a string duration like "30s", got %s`, val.Type().FriendlyName())
		}
		raw := val.AsString()
		d, err := time.ParseDuration(raw)
		if err != nil {
			return out, nil, fmt.Errorf(`timeout %q is not a valid Go duration (want "30s", "2m", "500ms", ...): %w`, raw, err)
		}
		if d < 0 {
			return out, nil, fmt.Errorf(`timeout %q must be positive`, raw)
		}
		out.Timeout = d
	}

	return out, remain, nil
}

// extractDependsOnRefs turns the RHS of `depends_on = [...]` into
// a slice of NodeIDs. The expression must be a tuple/list of
// traversals against the `action.*` or `component.*` namespaces;
// anything else (string literals, variable references, function
// calls) is rejected because graph dependencies need resolution
// to concrete node IDs at parse time, not runtime.
func extractDependsOnRefs(expr hcl.Expression, componentKey string) ([]NodeID, error) {
	// Every traversal inside the expression contributes a dep.
	// Using Expression.Variables() works uniformly for tuple
	// constructors, and for the degenerate single-value case the
	// user might write (depends_on = action.sql.foo).
	var deps []NodeID
	seen := make(map[NodeID]bool)
	for _, trav := range expr.Variables() {
		id, ok := traversalToNodeID(trav, componentKey)
		if !ok {
			return nil, fmt.Errorf("entry %q does not reference an action or component node", formatTraversal(trav))
		}
		if !seen[id] {
			seen[id] = true
			deps = append(deps, id)
		}
	}
	if len(deps) == 0 {
		return nil, fmt.Errorf("empty list — remove the attribute if no deps are needed")
	}
	return deps, nil
}

// formatTraversal renders an hcl.Traversal back to dotted form
// (`action.sql.foo.id`) for error messages. Purely cosmetic —
// `trav[0].SourceRange().String()` would point at source but
// isn't meaningful for scripted errors.
func formatTraversal(trav hcl.Traversal) string {
	if len(trav) == 0 {
		return "<empty>"
	}
	parts := []string{trav.RootName()}
	for _, step := range trav[1:] {
		if a, ok := step.(hcl.TraverseAttr); ok {
			parts = append(parts, a.Name)
		} else {
			parts = append(parts, "?")
		}
	}
	return strings.Join(parts, ".")
}
