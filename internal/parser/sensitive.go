package parser

import "github.com/zclconf/go-cty/cty"

// SensitiveMark is the cty mark on values that come from a sensitive
// variable. HCL carries it through expressions and function calls, so
// anything computed from such a value is marked too. The engine marks
// values only where it looks for the mark (plan output, component
// inputs) and unmarks them before they reach a provider.
const SensitiveMark = "sensitive"

// MarkSensitive returns vars with the variables names marks sensitive
// carrying SensitiveMark. vars itself is left alone.
func MarkSensitive(vars map[string]cty.Value, names map[string]bool) map[string]cty.Value {
	if len(names) == 0 {
		return vars
	}
	out := make(map[string]cty.Value, len(vars))
	for k, v := range vars {
		if names[k] {
			v = v.Mark(SensitiveMark)
		}
		out[k] = v
	}
	return out
}

// VariableSensitive reports whether v is declared `sensitive = true`.
func VariableSensitive(v *VariableBlock) (bool, error) {
	p, err := decodeVariable(v)
	return p.sensitive, err
}

// sensitiveVariables names the blocks declared `sensitive = true`.
func sensitiveVariables(blocks []*VariableBlock) (map[string]bool, error) {
	out := map[string]bool{}
	for _, v := range blocks {
		ok, err := VariableSensitive(v)
		if err != nil {
			return nil, err
		}
		if ok {
			out[v.Name] = true
		}
	}
	return out, nil
}
