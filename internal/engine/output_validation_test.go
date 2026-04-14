package engine

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/protocol"
)

// TestValidateOutputsHappyPath: values that already match the
// declared type pass through coerced and unchanged in meaning.
func TestValidateOutputsHappyPath(t *testing.T) {
	schema := protocol.ActionSchema{
		Outputs: &protocol.AttrSchema{
			Type: "object({status=number, body=string})",
		},
	}
	out, err := validateOutputs(schema, map[string]cty.Value{
		"status": cty.NumberIntVal(200),
		"body":   cty.StringVal("ok"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, _ := out["status"].AsBigFloat().Int64(); got != 200 {
		t.Errorf("status: got %v", out["status"])
	}
	if out["body"].AsString() != "ok" {
		t.Errorf("body: got %v", out["body"])
	}
}

// TestValidateOutputsCoerces: a string that parses as a number
// coerces to the declared number type. This is the standard
// cty.Convert behavior and makes providers with loose wire formats
// usable without requiring exact-type payloads.
func TestValidateOutputsCoerces(t *testing.T) {
	schema := protocol.ActionSchema{
		Outputs: &protocol.AttrSchema{Type: "object({status=number})"},
	}
	out, err := validateOutputs(schema, map[string]cty.Value{
		"status": cty.StringVal("200"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out["status"].Type().Equals(cty.Number) {
		t.Errorf("expected coercion to number, got %s", out["status"].Type().FriendlyName())
	}
}

// TestValidateOutputsRejectsUnknownKeys: a provider that returns
// attributes the schema doesn't declare fails loudly here rather
// than letting downstream references silently succeed on a typed
// expectation that was never met.
func TestValidateOutputsRejectsUnknownKeys(t *testing.T) {
	schema := protocol.ActionSchema{
		Outputs: &protocol.AttrSchema{Type: "object({status=number})"},
	}
	_, err := validateOutputs(schema, map[string]cty.Value{
		"status":     cty.NumberIntVal(200),
		"extra_junk": cty.StringVal("ignored?"),
	})
	if err == nil {
		t.Fatal("expected unknown-attribute error")
	}
	if !strings.Contains(err.Error(), "extra_junk") {
		t.Errorf("error should name the rejected attribute: %v", err)
	}
}

// TestValidateOutputsRejectsTypeMismatch: a value that cannot be
// coerced to the declared type is an error. Without this check, a
// plan that says `action.fetch.one.status + 1` would work at plan
// time and fail with a stack trace at runtime.
func TestValidateOutputsRejectsTypeMismatch(t *testing.T) {
	schema := protocol.ActionSchema{
		Outputs: &protocol.AttrSchema{Type: "object({status=number})"},
	}
	_, err := validateOutputs(schema, map[string]cty.Value{
		"status": cty.StringVal("not a number"),
	})
	if err == nil {
		t.Fatal("expected type-mismatch error")
	}
	if !strings.Contains(err.Error(), "status") {
		t.Errorf("error should name the offending attribute: %v", err)
	}
}

// TestValidateOutputsFillsMissingWithNull: missing declared attrs
// come through as typed nulls so downstream expressions see a
// consistently-shaped value.
func TestValidateOutputsFillsMissingWithNull(t *testing.T) {
	schema := protocol.ActionSchema{
		Outputs: &protocol.AttrSchema{Type: "object({status=number, body=string})"},
	}
	out, err := validateOutputs(schema, map[string]cty.Value{
		"status": cty.NumberIntVal(200),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out["body"].IsNull() {
		t.Errorf("expected typed null for missing body, got %v", out["body"])
	}
	if !out["body"].Type().Equals(cty.String) {
		t.Errorf("null should carry declared type: got %s", out["body"].Type().FriendlyName())
	}
}

// TestValidateOutputsDynamicPassthrough: actions declared with
// `outputs { type = any }` bypass validation. Plan-time typing
// already can't narrow them, so runtime matching has nothing to
// check against either.
func TestValidateOutputsDynamicPassthrough(t *testing.T) {
	schema := protocol.ActionSchema{
		Outputs: &protocol.AttrSchema{Type: "any"},
	}
	in := map[string]cty.Value{
		"anything": cty.StringVal("ok"),
		"else":     cty.NumberIntVal(7),
	}
	out, err := validateOutputs(schema, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("expected pass-through of 2 keys, got %d", len(out))
	}
}

// TestValidateOutputsNoSchema: actions without an output declaration
// accept whatever the provider returns. This mirrors pre-check
// behavior so adding the check isn't a breaking change for actions
// that legitimately have no declared shape.
func TestValidateOutputsNoSchema(t *testing.T) {
	schema := protocol.ActionSchema{Outputs: nil}
	in := map[string]cty.Value{"x": cty.StringVal("y")}
	out, err := validateOutputs(schema, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["x"].AsString() != "y" {
		t.Errorf("expected passthrough, got %v", out["x"])
	}
}
