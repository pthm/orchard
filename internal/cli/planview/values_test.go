package planview

import (
	"testing"

	"github.com/zclconf/go-cty/cty"
)

func TestFormatValue(t *testing.T) {
	cases := []struct {
		name string
		v    cty.Value
		want string
	}{
		{"string", cty.StringVal(`Acme "Ltd"`), `"Acme \"Ltd\""`},
		{"int", cty.NumberIntVal(30), "30"},
		{"float", cty.NumberFloatVal(1.5), "1.5"},
		{"bool", cty.True, "true"},
		{"null", cty.NullVal(cty.String), "null"},
		{"unknown", cty.UnknownVal(cty.String), "(known after run)"},
		{"dynamic unknown", cty.DynamicVal, "(known after run)"},
		{"object with nested unknown", cty.ObjectVal(map[string]cty.Value{
			"name":        cty.StringVal("Widget"),
			"merchant_id": cty.UnknownVal(cty.Number),
		}), `{ merchant_id = (known after run), name = "Widget" }`},
		{"map with odd key", cty.MapVal(map[string]cty.Value{"a b": cty.NumberIntVal(1)}), `{ "a b" = 1 }`},
		{"empty object", cty.EmptyObjectVal, "{}"},
		{"list", cty.ListVal([]cty.Value{cty.StringVal("a"), cty.UnknownVal(cty.String)}), `["a", (known after run)]`},
		{"empty tuple", cty.EmptyTupleVal, "[]"},
		{"sensitive", cty.StringVal("s3cret").Mark("sensitive"), "(sensitive)"},
		{"object with sensitive field", cty.ObjectVal(map[string]cty.Value{
			"name":  cty.StringVal("Acme"),
			"token": cty.StringVal("s3cret").Mark("sensitive"),
		}), `{ name = "Acme", token = (sensitive) }`},
		{"multi-line string", cty.StringVal("SELECT 1;\nSELECT 2;\n"), "<<EOT\n    SELECT 1;\n    SELECT 2;\n  EOT"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatValue(c.v, "  "); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
