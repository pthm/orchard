// Package planview renders a parsed scenario + its execution
// graph into a human-readable plan. The output is designed to
// answer the question "what will happen if I run this?" — not
// just the DAG, but the providers, variables, dependency shape,
// and what each action is going to do.
//
// Styling goes through internal/cli/render so a non-TTY pipe
// gets clean, uncolored text automatically.
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

// Render writes the plan to w. Section order: Scenario header,
// Providers, Variables, Components (if any), Execution order,
// Outputs, final status line. Each section is elided when empty.
func Render(w io.Writer, ps *parser.ParsedScenario, g *graph.Graph) {
	fmt.Fprintf(w, "%s %s\n\n",
		render.Header.Render("▸ Scenario"),
		render.Bold.Render(ps.Scenario.Name))

	renderProviders(w, ps)
	renderVariables(w, ps)
	renderComponents(w, ps)
	renderExecutionOrder(w, ps, g)
	renderOutputs(w, ps)
}

// RenderSuccess writes the final "plan is valid" line.
func RenderSuccess(w io.Writer) {
	fmt.Fprintf(w, "%s\n", render.Success.Render("✓ Plan is valid"))
}

// --- providers -------------------------------------------------

func renderProviders(w io.Writer, ps *parser.ParsedScenario) {
	if len(ps.RequiredProviders) == 0 {
		return
	}
	section(w, "Providers", len(ps.RequiredProviders))

	names := sortedRequiredProviderNames(ps.RequiredProviders)
	width := longestString(names)
	for _, name := range names {
		spec := ps.RequiredProviders[name]
		fmt.Fprintf(w, "  %s  %s\n",
			render.Bold.Render(padRight(name, width)),
			render.Kind.Render(spec.Source))
		// Print configured attributes, one per line, muted.
		cfg := ps.ProviderConfigs[name]
		if len(cfg) > 0 {
			keys := sortedKeys(cfg)
			for _, k := range keys {
				fmt.Fprintf(w, "    %s %s %s\n",
					render.Muted.Render(padRight(k, longestString(keys))),
					render.Muted.Render("="),
					render.Value.Render(formatValue(cfg[k])))
			}
		}
	}
	fmt.Fprintln(w)
}

// --- variables -------------------------------------------------

func renderVariables(w io.Writer, ps *parser.ParsedScenario) {
	if len(ps.Variables) == 0 {
		return
	}
	section(w, "Variables", len(ps.Variables))

	// Build a lookup from the declared variable blocks so we can
	// print each variable's type even when it wasn't declared.
	declTypes := make(map[string]string, len(ps.Scenario.Variables))
	for _, v := range ps.Scenario.Variables {
		t, err := parser.ResolveVariableType(v)
		if err != nil || t == cty.DynamicPseudoType {
			continue
		}
		declTypes[v.Name] = t.FriendlyName()
	}

	names := sortedKeys(ps.Variables)
	nameWidth := longestString(names)
	// Only reserve type-column width if at least one variable
	// declares a type. Scenarios that leave types implicit get a
	// cleaner two-column "name = value" layout.
	typeWidth := 0
	for _, tn := range declTypes {
		if len(tn)+2 > typeWidth {
			typeWidth = len(tn) + 2
		}
	}
	for _, name := range names {
		typeCol := ""
		if tn, ok := declTypes[name]; ok {
			typeCol = "(" + tn + ")"
		}
		if typeWidth > 0 {
			fmt.Fprintf(w, "  %s  %s  %s\n",
				render.Bold.Render(padRight(name, nameWidth)),
				render.Muted.Render(padRight(typeCol, typeWidth)),
				render.Value.Render(formatValue(ps.Variables[name])))
		} else {
			fmt.Fprintf(w, "  %s  %s\n",
				render.Bold.Render(padRight(name, nameWidth)),
				render.Value.Render(formatValue(ps.Variables[name])))
		}
	}
	fmt.Fprintln(w)
}

// --- components ------------------------------------------------

func renderComponents(w io.Writer, ps *parser.ParsedScenario) {
	if len(ps.Components) == 0 {
		return
	}
	section(w, "Components", len(ps.Components))

	keys := sortedComponentKeys(ps.Components)
	for _, key := range keys {
		comp := ps.Components[key]
		fmt.Fprintf(w, "  %s  %s\n",
			render.Bold.Render(key),
			render.Muted.Render("source: "+comp.Ref.Source))

		// inputs
		inputKeys := extractStaticInputKeys(comp.Ref.Inputs)
		if len(inputKeys) > 0 {
			fmt.Fprintf(w, "    %s\n", render.Muted.Render("inputs:"))
			for _, k := range inputKeys {
				fmt.Fprintf(w, "      %s\n", k)
			}
		}

		// outputs (names only — values are runtime)
		if len(comp.Def.Outputs) > 0 {
			names := make([]string, 0, len(comp.Def.Outputs))
			for _, o := range comp.Def.Outputs {
				names = append(names, o.Name)
			}
			sort.Strings(names)
			fmt.Fprintf(w, "    %s %s\n",
				render.Muted.Render("outputs:"),
				render.Muted.Render(strings.Join(names, ", ")))
		}
	}
	fmt.Fprintln(w)
}

// --- execution order -------------------------------------------

func renderExecutionOrder(w io.Writer, _ *parser.ParsedScenario, g *graph.Graph) {
	if len(g.Order) == 0 {
		return
	}

	// Count actions vs components for the header.
	actions, components := 0, 0
	for _, id := range g.Order {
		switch g.Nodes[id].Kind {
		case graph.ActionNode:
			actions++
		case graph.ComponentNode:
			components++
		}
	}
	fmt.Fprintf(w, "%s  %s\n",
		render.Label.Render("Execution plan"),
		render.Muted.Render(fmt.Sprintf("(%s, %s)",
			pluralize(actions, "action", "actions"),
			pluralize(components, "component", "components"))))
	fmt.Fprintln(w,
		render.Muted.Render(strings.Repeat("─", 40)))

	// Width for step numbers.
	stepWidth := len(fmt.Sprintf("%d", len(g.Order)))

	for i, nodeID := range g.Order {
		node := g.Nodes[nodeID]
		step := render.Muted.Render(padLeft(fmt.Sprintf("%d.", i+1), stepWidth+1))

		switch node.Kind {
		case graph.ActionNode:
			renderActionStep(w, step, node)
		case graph.ComponentNode:
			renderComponentStep(w, step, node)
		}

		if len(node.Dependencies) > 0 {
			deps := make([]string, len(node.Dependencies))
			for i, d := range node.Dependencies {
				deps[i] = string(d)
			}
			sort.Strings(deps)
			fmt.Fprintf(w, "     %s %s\n",
				render.Muted.Render("after"),
				render.Ref.Render(strings.Join(deps, ", ")))
		}
	}
	fmt.Fprintln(w)
}

func renderActionStep(w io.Writer, step string, node *graph.Node) {
	kind := render.Kind.Render("[action]")
	providerSuffix := ""
	if node.ProviderAlias != "" {
		providerSuffix = "  " + render.Muted.Render("via "+node.ProviderAlias)
	}
	fmt.Fprintf(w, "  %s %s %s%s\n",
		step,
		kind,
		render.Bold.Render(string(node.ID)),
		providerSuffix)
}

func renderComponentStep(w io.Writer, step string, node *graph.Node) {
	kind := render.Kind.Render("[component]")
	fmt.Fprintf(w, "  %s %s %s\n",
		step,
		kind,
		render.Bold.Render(string(node.ID)))
}

// --- outputs ---------------------------------------------------

func renderOutputs(w io.Writer, ps *parser.ParsedScenario) {
	if len(ps.Scenario.Outputs) == 0 {
		return
	}
	section(w, "Outputs", len(ps.Scenario.Outputs))

	names := make([]string, 0, len(ps.Scenario.Outputs))
	for _, o := range ps.Scenario.Outputs {
		names = append(names, o.Name)
	}
	sort.Strings(names)
	byName := make(map[string]*parser.OutputBlock, len(ps.Scenario.Outputs))
	for _, o := range ps.Scenario.Outputs {
		byName[o.Name] = o
	}
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

// firstReference returns the first `action.X.Y.Z` or
// `component.X.Y.Z` traversal the expression references, as a
// dotted string. Empty when the expression is a literal or
// references nothing graph-level.
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

func section(w io.Writer, label string, count int) {
	fmt.Fprintf(w, "%s  %s\n",
		render.Label.Render(label),
		render.Muted.Render(fmt.Sprintf("(%d)", count)))
	fmt.Fprintln(w, render.Muted.Render(strings.Repeat("─", 40)))
}

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
	// We don't evaluate (may reference action outputs). Use the
	// ExprAsKeyword-style key extraction via hcl.ExprMap when
	// available; otherwise render the raw expression.
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

func sortedKeys(m map[string]cty.Value) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedRequiredProviderNames(m map[string]parser.RequiredProviderSpec) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedComponentKeys(m map[string]*parser.ParsedComponent) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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

func padLeft(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

func formatValue(v cty.Value) string {
	if v.IsNull() {
		return "null"
	}
	switch v.Type() {
	case cty.String:
		return v.AsString()
	case cty.Number:
		bf := v.AsBigFloat()
		if bf.IsInt() {
			i, _ := bf.Int64()
			return fmt.Sprintf("%d", i)
		}
		f, _ := bf.Float64()
		return fmt.Sprintf("%g", f)
	case cty.Bool:
		if v.True() {
			return "true"
		}
		return "false"
	default:
		return v.GoString()
	}
}
