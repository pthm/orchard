package parser

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// sensitiveVariables names the blocks declared `sensitive = true`.
func sensitiveVariables(blocks []*VariableBlock) (map[string]bool, error) {
	out := map[string]bool{}
	for _, v := range blocks {
		p, err := decodeVariable(v)
		if err != nil {
			return nil, err
		}
		if p.sensitive {
			out[v.Name] = true
		}
	}
	return out, nil
}

// componentSensitive names comp's variables that hold a sensitive
// value: declared sensitive, or given an input whose expression
// references a sensitive scenario variable. An inputs expression that
// isn't an object literal ({ ... }) can't be split per key, so if it
// references one, every variable counts.
//
// ponytail: static references only; a secret that reaches an input
// through an action's outputs isn't caught.
func componentSensitive(comp *ParsedComponent, scenario map[string]bool) (map[string]bool, error) {
	out, err := sensitiveVariables(comp.Def.Variables)
	if err != nil {
		return nil, err
	}
	if len(scenario) == 0 || comp.Ref.Inputs == nil {
		return out, nil
	}
	content, _, diags := comp.Ref.Inputs.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: "inputs"}},
	})
	if diags.HasErrors() {
		return out, nil // reported when the inputs are decoded
	}
	attr, ok := content.Attributes["inputs"]
	if !ok {
		return out, nil
	}
	obj, ok := attr.Expr.(*hclsyntax.ObjectConsExpr)
	if !ok {
		if referencesAny(attr.Expr, scenario) {
			for _, v := range comp.Def.Variables {
				out[v.Name] = true
			}
		}
		return out, nil
	}
	for _, item := range obj.Items {
		key := hcl.ExprAsKeyword(item.KeyExpr)
		if key == "" {
			if v, diags := item.KeyExpr.Value(nil); !diags.HasErrors() && v.Type() == cty.String {
				key = v.AsString()
			}
		}
		if key != "" && referencesAny(item.ValueExpr, scenario) {
			out[key] = true
		}
	}
	return out, nil
}

// referencesAny reports whether expr reads var.<name> for a name in names.
func referencesAny(expr hcl.Expression, names map[string]bool) bool {
	for _, tr := range expr.Variables() {
		if tr.RootName() != "var" || len(tr) < 2 {
			continue
		}
		if a, ok := tr[1].(hcl.TraverseAttr); ok && names[a.Name] {
			return true
		}
	}
	return false
}
