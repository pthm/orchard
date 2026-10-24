package planview

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/cli/render"
)

// unknownText stands in for a value that depends on another action.
const unknownText = "(known after run)"

// renderValues lists an action's evaluated attributes, one per line
// with the `=` signs aligned.
func renderValues(w io.Writer, indent string, attrs map[string]cty.Value) {
	if len(attrs) == 0 {
		return
	}
	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	width := longestString(names)
	for _, name := range names {
		_, _ = fmt.Fprintf(w, "%s%s = %s\n",
			indent,
			render.Muted.Render(padRight(name, width)),
			formatValue(attrs[name], indent))
	}
}

// formatValue renders a cty value in HCL-like syntax on one line:
// objects and lists inline, any unknown (however deeply nested) as
// "(known after run)". A multi-line string becomes a heredoc whose
// lines sit one step past indent. ctyjson can't be used: it refuses
// unknown values.
func formatValue(v cty.Value, indent string) string {
	v, _ = v.UnmarkDeep()
	switch {
	case !v.IsKnown():
		return unknownText
	case v.IsNull():
		return "null"
	}
	t := v.Type()
	switch {
	case t == cty.String:
		s := v.AsString()
		if !strings.Contains(strings.TrimSuffix(s, "\n"), "\n") {
			return strconv.Quote(s)
		}
		inner := indent + indentUnit
		lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
		return "<<EOT\n" + inner + strings.Join(lines, "\n"+inner) + "\n" + indent + "EOT"
	case t == cty.Number:
		return v.AsBigFloat().Text('f', -1)
	case t == cty.Bool:
		return strconv.FormatBool(v.True())
	case t.IsObjectType() || t.IsMapType():
		if v.LengthInt() == 0 {
			return "{}"
		}
		parts := make([]string, 0, v.LengthInt())
		for it := v.ElementIterator(); it.Next(); {
			k, ev := it.Element()
			key := k.AsString()
			if !hclsyntax.ValidIdentifier(key) {
				key = strconv.Quote(key)
			}
			parts = append(parts, key+" = "+formatValue(ev, indent))
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	case t.IsListType() || t.IsSetType() || t.IsTupleType():
		parts := make([]string, 0, v.LengthInt())
		for it := v.ElementIterator(); it.Next(); {
			_, ev := it.Element()
			parts = append(parts, formatValue(ev, indent))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return v.GoString()
}
