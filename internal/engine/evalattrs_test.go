package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/protocol"
)

// parseBody is a test helper that parses an HCL snippet and returns
// the root body — what the engine passes into evalAttrs.
func parseBody(t *testing.T, src string) hcl.Body {
	t.Helper()
	p := hclparse.NewParser()
	file, diags := p.ParseHCL([]byte(src), "test.hcl")
	if diags.HasErrors() {
		t.Fatalf("parse: %s", diags.Error())
	}
	return file.Body
}

func TestEvalAttrsRequiredAndPresent(t *testing.T) {
	body := parseBody(t, `query = "SELECT 1"`)
	schema := protocol.ActionSchema{Attrs: map[string]protocol.AttrSchema{
		"query": {Type: "string", Required: true},
	}}

	out, err := evalAttrs(body, schema, &hcl.EvalContext{}, 0, "test")
	if err != nil {
		t.Fatalf("evalAttrs: %v", err)
	}
	if out["query"].AsString() != "SELECT 1" {
		t.Fatalf("query: got %q", out["query"].AsString())
	}
}

func TestEvalAttrsMissingRequired(t *testing.T) {
	body := parseBody(t, ``)
	schema := protocol.ActionSchema{Attrs: map[string]protocol.AttrSchema{
		"query": {Type: "string", Required: true},
	}}
	_, err := evalAttrs(body, schema, &hcl.EvalContext{}, 0, "test")
	if err == nil || !strings.Contains(err.Error(), "query") {
		t.Fatalf("expected missing-required error, got %v", err)
	}
}

func TestEvalAttrsOptionalMissingNoDefault(t *testing.T) {
	body := parseBody(t, `required_attr = "x"`)
	schema := protocol.ActionSchema{Attrs: map[string]protocol.AttrSchema{
		"required_attr": {Type: "string", Required: true},
		"timeout":       {Type: "number"},
	}}
	out, err := evalAttrs(body, schema, &hcl.EvalContext{}, 0, "test")
	if err != nil {
		t.Fatalf("evalAttrs: %v", err)
	}
	if _, present := out["timeout"]; present {
		t.Fatalf("optional-with-no-default should be absent, got %v", out["timeout"])
	}
}

func TestEvalAttrsOptionalMissingWithDefault(t *testing.T) {
	body := parseBody(t, `required_attr = "x"`)
	schema := protocol.ActionSchema{Attrs: map[string]protocol.AttrSchema{
		"required_attr": {Type: "string", Required: true},
		"timeout":       {Type: "number", Default: json.RawMessage(`30`)},
	}}
	out, err := evalAttrs(body, schema, &hcl.EvalContext{}, 0, "test")
	if err != nil {
		t.Fatalf("evalAttrs: %v", err)
	}
	got := out["timeout"]
	if got.IsNull() || !got.AsBigFloat().IsInt() {
		t.Fatalf("timeout default: got %#v", got)
	}
	n, _ := got.AsBigFloat().Int64()
	if n != 30 {
		t.Fatalf("timeout default: got %d want 30", n)
	}
}

func TestEvalAttrsRejectsUnknownAttr(t *testing.T) {
	body := parseBody(t, `
query = "SELECT 1"
mystery = true
`)
	schema := protocol.ActionSchema{Attrs: map[string]protocol.AttrSchema{
		"query": {Type: "string", Required: true},
	}}
	_, err := evalAttrs(body, schema, &hcl.EvalContext{}, 0, "test")
	if err == nil || !strings.Contains(err.Error(), "mystery") {
		t.Fatalf("expected unknown-attribute error, got %v", err)
	}
}

func TestEvalAttrsTypeCoercion(t *testing.T) {
	// Declared type is number; the HCL expression yields a string "42".
	// convert.Convert coerces the value so providers receive a number.
	body := parseBody(t, `count = "42"`)
	schema := protocol.ActionSchema{Attrs: map[string]protocol.AttrSchema{
		"count": {Type: "number"},
	}}
	out, err := evalAttrs(body, schema, &hcl.EvalContext{}, 0, "test")
	if err != nil {
		t.Fatalf("evalAttrs: %v", err)
	}
	if out["count"].Type() != cty.Number {
		t.Fatalf("expected number, got %s", out["count"].Type().FriendlyName())
	}
}

func TestEvalAttrsDynamicPassthrough(t *testing.T) {
	// type=any accepts whatever shape the user writes.
	body := parseBody(t, `values = { a = 1, b = "two" }`)
	schema := protocol.ActionSchema{Attrs: map[string]protocol.AttrSchema{
		"values": {Type: "any"},
	}}
	out, err := evalAttrs(body, schema, &hcl.EvalContext{}, 0, "test")
	if err != nil {
		t.Fatalf("evalAttrs: %v", err)
	}
	if out["values"].GetAttr("b").AsString() != "two" {
		t.Fatalf("values.b: got %#v", out["values"].GetAttr("b"))
	}
}

func TestEvalAttrsUncoerceableType(t *testing.T) {
	body := parseBody(t, `count = "not a number"`)
	schema := protocol.ActionSchema{Attrs: map[string]protocol.AttrSchema{
		"count": {Type: "number"},
	}}
	_, err := evalAttrs(body, schema, &hcl.EvalContext{}, 0, "test")
	if err == nil || !strings.Contains(err.Error(), "count") {
		t.Fatalf("expected coercion error, got %v", err)
	}
}

func TestEvalAttrsNilBodyWithNoAttrs(t *testing.T) {
	out, err := evalAttrs(nil, protocol.ActionSchema{}, &hcl.EvalContext{}, 0, "test")
	if err != nil {
		t.Fatalf("evalAttrs: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty map, got %v", out)
	}
}

func TestEvalAttrsNilBodyWithRequiredAttr(t *testing.T) {
	schema := protocol.ActionSchema{Attrs: map[string]protocol.AttrSchema{
		"query": {Type: "string", Required: true},
	}}
	_, err := evalAttrs(nil, schema, &hcl.EvalContext{}, 0, "test")
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("expected required-missing error, got %v", err)
	}
}
