// Package planview renders a parsed scenario + its execution
// graph into a human-readable plan that answers "what will
// happen if I run this?" — the nodes that will execute, their
// dependencies, the providers they route to, and the outputs
// that will be produced.
//
// Shape: a Terraform-style block listing (`action "<type>"
// "<name>"`, `component "<type>" "<name>"`) laid out as a tree
// of primary-parent indentation, so the dependency graph reads
// top-down. Secondary parents surface as "also after: ..." lines
// under the affected node.
//
// Styling goes through internal/cli/render so non-TTY pipes get
// clean, uncolored text.
package planview

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/cli/render"
	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
)

// ProviderOf returns the local alias of the provider that will
// handle the given action node. Empty string when the node isn't
// an action or the routing isn't resolvable (ambiguous, missing).
// Pass nil to render without provider annotations.
type ProviderOf func(*graph.Node) string

// ValuesOf returns an action node's evaluated attributes, which the
// plan lists under the action. Pass nil to render without them.
type ValuesOf func(*graph.Node) map[string]cty.Value

// Render writes the plan to w.
func Render(w io.Writer, ps *parser.ParsedScenario, g *graph.Graph, providerOf ProviderOf, valuesOf ValuesOf) {
	if providerOf == nil {
		providerOf = func(*graph.Node) string { return "" }
	}
	if valuesOf == nil {
		valuesOf = func(*graph.Node) map[string]cty.Value { return nil }
	}

	_, _ = fmt.Fprintf(w, "%s %s\n",
		render.Header.Render("▸"),
		render.Bold.Render(ps.Scenario.Name))
	_, _ = fmt.Fprintln(w,
		render.Muted.Render("  "+summary(ps, g)))
	_, _ = fmt.Fprintln(w)

	forest := buildForest(g)
	for i, root := range forest {
		if i > 0 {
			_, _ = fmt.Fprintln(w)
		}
		renderNode(w, ps, g, providerOf, valuesOf, root, 0)
	}

	renderOutputs(w, ps)
}

// RenderSuccess writes the final success line.
func RenderSuccess(w io.Writer) {
	_, _ = fmt.Fprintf(w, "%s\n", render.Success.Render("✓ Plan is valid"))
}

// summary returns a one-line description of the plan's shape:
// "2 actions, 1 component — via postgres, echo".
func summary(ps *parser.ParsedScenario, g *graph.Graph) string {
	actions, components := 0, 0
	for _, id := range g.Order {
		switch g.Nodes[id].Kind {
		case graph.ActionNode:
			actions++
		case graph.ComponentNode:
			components++
		}
	}
	parts := []string{pluralize(actions, "action", "actions")}
	if components > 0 {
		parts = append(parts, pluralize(components, "component", "components"))
	}
	summary := strings.Join(parts, ", ")

	if len(ps.RequiredProviders) > 0 {
		names := make([]string, 0, len(ps.RequiredProviders))
		for name := range ps.RequiredProviders {
			names = append(names, name)
		}
		sort.Strings(names)
		summary += " — via " + strings.Join(names, ", ")
	}
	return summary
}

// --- tree construction -----------------------------------------

// displayNode is one entry in the plan tree. It wraps a graph
// node plus any children (nodes whose primary parent is this one)
// and the IDs of secondary parents that can't be shown via
// indentation alone.
type displayNode struct {
	id               graph.NodeID
	node             *graph.Node
	children         []*displayNode
	secondaryParents []graph.NodeID
}

// buildForest builds the primary-parent tree over the "top-level"
// nodes of the graph. Component-internal actions are excluded
// from the top-level tree — they render inside their component's
// block instead.
//
// The primary parent of a node is the parent with the highest
// index in g.Order. That gives consistent behaviour with the
// engine's topo walk (the "closest" ancestor wins) and makes the
// rendered tree feel intuitive: each node sits under the thing
// the engine will have just finished before running it.
func buildForest(g *graph.Graph) []*displayNode {
	topo := make(map[graph.NodeID]int, len(g.Order))
	for i, id := range g.Order {
		topo[id] = i
	}

	// Top-level filter: drop component-internal actions. Component
	// nodes themselves stay — they are the block headers.
	topLevel := make(map[graph.NodeID]bool, len(g.Nodes))
	for id, n := range g.Nodes {
		if n.Kind == graph.ActionNode && n.ComponentKey != "" {
			continue
		}
		topLevel[id] = true
	}

	nodesByID := make(map[graph.NodeID]*displayNode, len(topLevel))
	for id := range topLevel {
		nodesByID[id] = &displayNode{id: id, node: g.Nodes[id]}
	}

	for id := range topLevel {
		n := g.Nodes[id]
		// Collect top-level parents only. An internal action's
		// parent chain bubbles up to the component node via the
		// component-internal dependency graph; for the top-level
		// tree we skip those edges.
		var topParents []graph.NodeID
		for _, dep := range n.Dependencies {
			if topLevel[dep] {
				topParents = append(topParents, dep)
			}
		}
		if len(topParents) == 0 {
			continue
		}
		// Pick the parent with the highest topo index as the primary.
		sort.SliceStable(topParents, func(i, j int) bool {
			return topo[topParents[i]] > topo[topParents[j]]
		})
		primary := topParents[0]
		nodesByID[primary].children = append(nodesByID[primary].children, nodesByID[id])
		// Rest are secondaries — shown as "also after" on the child.
		nodesByID[id].secondaryParents = append(nodesByID[id].secondaryParents, topParents[1:]...)
	}

	// Roots: nodes with no top-level parent.
	var roots []*displayNode
	for id, dn := range nodesByID {
		hasTopParent := false
		for _, dep := range g.Nodes[id].Dependencies {
			if topLevel[dep] {
				hasTopParent = true
				break
			}
		}
		if !hasTopParent {
			roots = append(roots, dn)
		}
	}
	// Stable ordering: sort roots by topo index. Same for each
	// child list so nested layout follows topological time.
	sortByTopo(roots, topo)
	var fixOrder func(dn *displayNode)
	fixOrder = func(dn *displayNode) {
		sortByTopo(dn.children, topo)
		for _, c := range dn.children {
			fixOrder(c)
		}
	}
	for _, r := range roots {
		fixOrder(r)
	}
	return roots
}

func sortByTopo(ns []*displayNode, topo map[graph.NodeID]int) {
	sort.SliceStable(ns, func(i, j int) bool {
		return topo[ns[i].id] < topo[ns[j].id]
	})
}

// --- node rendering --------------------------------------------

const indentUnit = "  "

func renderNode(
	w io.Writer,
	ps *parser.ParsedScenario,
	g *graph.Graph,
	providerOf ProviderOf,
	valuesOf ValuesOf,
	dn *displayNode,
	depth int,
) {
	indent := strings.Repeat(indentUnit, depth)

	switch dn.node.Kind {
	case graph.ActionNode:
		renderActionHeader(w, indent, dn.node, providerOf)
		renderValues(w, indent+indentUnit, valuesOf(dn.node))
		renderSecondaryParents(w, indent+indentUnit, dn.secondaryParents)
	case graph.ComponentNode:
		renderComponentBlock(w, ps, g, providerOf, valuesOf, indent, dn)
		renderSecondaryParents(w, indent+indentUnit, dn.secondaryParents)
		// Blank line between a component's interior (above) and its
		// dependent children (below). The ↳ glyph and the blank
		// line together are what the eye uses to tell "inside the
		// component" from "runs after the component".
		if len(dn.children) > 0 {
			_, _ = fmt.Fprintln(w)
		}
	}

	for _, child := range dn.children {
		renderNode(w, ps, g, providerOf, valuesOf, child, depth+1)
	}
}

func renderActionHeader(w io.Writer, indent string, n *graph.Node, providerOf ProviderOf) {
	typ, name := parseActionID(string(n.ID))
	header := formatActionHeader(typ, name)
	suffix := ""
	if alias := providerOf(n); alias != "" {
		suffix = "  " + render.Muted.Render("via ") + render.Ref.Render(alias)
	}
	_, _ = fmt.Fprintf(w, "%s%s%s\n", indent, header, suffix)
}

func renderComponentBlock(
	w io.Writer,
	ps *parser.ParsedScenario,
	g *graph.Graph,
	providerOf ProviderOf,
	valuesOf ValuesOf,
	indent string,
	dn *displayNode,
) {
	key := dn.node.ComponentKey
	comp := ps.Components[key]

	typ, name := splitComponentKey(key)
	_, _ = fmt.Fprintf(w, "%s%s\n", indent, formatComponentHeader(typ, name))

	// Component interior sits at indent+1 — the same indent
	// dependents would get, but the ↳ glyph plus the blank line
	// that renderNode inserts before rendering dependents is what
	// visually separates "inside the component" from "runs after".
	interior := indent + indentUnit
	if comp != nil && comp.Ref.Source != "" {
		_, _ = fmt.Fprintf(w, "%s%s  %s\n",
			interior,
			render.Muted.Render("source"),
			render.Ref.Render(comp.Ref.Source))
	}
	if comp != nil {
		if keys := extractStaticInputKeys(comp.Ref.Inputs); len(keys) > 0 {
			_, _ = fmt.Fprintf(w, "%s%s  %s\n",
				interior,
				render.Muted.Render("inputs"),
				strings.Join(keys, ", "))
		}
	}
	for _, id := range g.Order {
		child := g.Nodes[id]
		if child.Kind != graph.ActionNode || child.ComponentKey != key {
			continue
		}
		typ, name := parseActionID(string(child.ID))
		suffix := ""
		if alias := providerOf(child); alias != "" {
			suffix = "  " + render.Muted.Render("via ") + render.Ref.Render(alias)
		}
		fmt.Fprintf(w, "%s%s %s%s\n",
			interior,
			render.Muted.Render("↳"),
			formatActionHeader(typ, name),
			suffix)
		// Past the "↳ " glyph, in line with the action keyword.
		renderValues(w, interior+indentUnit+indentUnit, valuesOf(child))
	}
}

func renderSecondaryParents(w io.Writer, indent string, parents []graph.NodeID) {
	if len(parents) == 0 {
		return
	}
	names := make([]string, len(parents))
	for i, p := range parents {
		names[i] = string(p)
	}
	sort.Strings(names)
	fmt.Fprintf(w, "%s%s  %s\n",
		indent,
		render.Muted.Render("also after"),
		render.Ref.Render(strings.Join(names, ", ")))
}

// formatActionHeader renders `action "<type>" "<name>"` with the
// keyword in a dedicated color and labels bold.
func formatActionHeader(typ, name string) string {
	return render.Kind.Render("action") + " " +
		render.Bold.Render(quote(typ)) + " " +
		render.Bold.Render(quoteLabel(name))
}

// formatComponentHeader renders `component "<type>" "<name>"` —
// same treatment, different keyword color so the eye separates
// component blocks from action leaves.
func formatComponentHeader(typ, name string) string {
	return render.Label.Render("component") + " " +
		render.Bold.Render(quote(typ)) + " " +
		render.Bold.Render(quoteLabel(name))
}

func quote(s string) string {
	return `"` + s + `"`
}

// quoteLabel quotes a block name, keeping a count / for_each instance
// suffix outside the quotes: `"payment"[0]`, `"site"["eu"]`.
func quoteLabel(s string) string {
	if base, rest, ok := strings.Cut(s, "["); ok {
		return quote(base) + "[" + rest
	}
	return quote(s)
}

// parseActionID splits an "action.<type>.<name>" NodeID into its
// parts. Component-internal action IDs look like
// "component.<key>.action.<type>.<name>"; we also handle those.
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

// --- outputs ---------------------------------------------------

func renderOutputs(w io.Writer, ps *parser.ParsedScenario) {
	if len(ps.Scenario.Outputs) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, render.Label.Render("Outputs"))

	names := make([]string, 0, len(ps.Scenario.Outputs))
	byName := make(map[string]*parser.OutputBlock, len(ps.Scenario.Outputs))
	for _, o := range ps.Scenario.Outputs {
		names = append(names, o.Name)
		byName[o.Name] = o
	}
	sort.Strings(names)
	nameWidth := longestString(names)

	for _, name := range names {
		src := firstReference(byName[name].Value)
		if src == "" {
			fmt.Fprintf(w, "  %s\n", render.Bold.Render(name))
			continue
		}
		fmt.Fprintf(w, "  %s  %s  %s\n",
			render.Bold.Render(padRight(name, nameWidth)),
			render.Muted.Render("←"),
			render.Ref.Render(src))
	}
	fmt.Fprintln(w)
}

func firstReference(expr hcl.Expression) string {
	for _, t := range expr.Variables() {
		root := t.RootName()
		if root != "action" && root != "component" {
			continue
		}
		parts := []string{root}
		for _, step := range t[1:] {
			name, ok := traverseAttrName(step)
			if !ok {
				break
			}
			parts = append(parts, name)
		}
		return strings.Join(parts, ".")
	}
	return ""
}

func traverseAttrName(t hcl.Traverser) (string, bool) {
	if a, ok := t.(hcl.TraverseAttr); ok {
		return a.Name, true
	}
	return "", false
}

// --- helpers ---------------------------------------------------

func extractStaticInputKeys(body hcl.Body) []string {
	if body == nil {
		return nil
	}
	attrs, diags := body.JustAttributes()
	if diags.HasErrors() {
		return nil
	}
	inputs, ok := attrs["inputs"]
	if !ok {
		return nil
	}
	pairs, diags := hcl.ExprMap(inputs.Expr)
	if diags.HasErrors() {
		return nil
	}
	keys := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if name := hcl.ExprAsKeyword(p.Key); name != "" {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	return keys
}

func longestString(ss []string) int {
	n := 0
	for _, s := range ss {
		if len(s) > n {
			n = len(s)
		}
	}
	return n
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}
