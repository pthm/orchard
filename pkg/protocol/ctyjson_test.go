package protocol

import (
	"encoding/json"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

func TestMarshalUnmarshalPrimitives(t *testing.T) {
	cases := []struct {
		name string
		val  cty.Value
		t    cty.Type
	}{
		{"string", cty.StringVal("hello"), cty.String},
		{"number", cty.NumberIntVal(42), cty.Number},
		{"bool", cty.BoolVal(true), cty.Bool},
		{"null string", cty.NullVal(cty.String), cty.String},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := MarshalValueAs(c.val, c.t)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got, err := UnmarshalValue(raw, c.t)
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !got.RawEquals(c.val) {
				t.Fatalf("round-trip: got %#v want %#v", got, c.val)
			}
		})
	}
}

func TestMarshalUnmarshalCollections(t *testing.T) {
	listT := cty.List(cty.String)
	list := cty.ListVal([]cty.Value{cty.StringVal("a"), cty.StringVal("b")})
	raw, err := MarshalValueAs(list, listT)
	if err != nil {
		t.Fatalf("marshal list: %v", err)
	}
	if string(raw) != `["a","b"]` {
		t.Fatalf("list payload: %s", raw)
	}
	got, err := UnmarshalValue(raw, listT)
	if err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if !got.RawEquals(list) {
		t.Fatalf("list round-trip mismatch")
	}

	objT := cty.Object(map[string]cty.Type{"id": cty.Number, "name": cty.String})
	obj := cty.ObjectVal(map[string]cty.Value{
		"id":   cty.NumberIntVal(7),
		"name": cty.StringVal("Acme"),
	})
	raw, err = MarshalValueAs(obj, objT)
	if err != nil {
		t.Fatalf("marshal object: %v", err)
	}
	got, err = UnmarshalValue(raw, objT)
	if err != nil {
		t.Fatalf("unmarshal object: %v", err)
	}
	if !got.RawEquals(obj) {
		t.Fatalf("object round-trip mismatch: got %#v", got)
	}
}

func TestMarshalAttrsAndBack(t *testing.T) {
	attrs := map[string]cty.Value{
		"query":   cty.StringVal("SELECT 1"),
		"timeout": cty.NumberIntVal(5),
	}
	raw, err := MarshalAttrs(attrs)
	if err != nil {
		t.Fatalf("MarshalAttrs: %v", err)
	}
	got, err := UnmarshalAttrs(raw, map[string]cty.Type{
		"query":   cty.String,
		"timeout": cty.Number,
	})
	if err != nil {
		t.Fatalf("UnmarshalAttrs: %v", err)
	}
	if !got["query"].RawEquals(attrs["query"]) || !got["timeout"].RawEquals(attrs["timeout"]) {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestUnmarshalAttrsDynamicFallback(t *testing.T) {
	// Attribute present on the wire but absent from the type map should
	// decode as a dynamic value, not fail.
	raw := map[string]json.RawMessage{"extra": json.RawMessage(`"surprise"`)}
	got, err := UnmarshalAttrs(raw, map[string]cty.Type{})
	if err != nil {
		t.Fatalf("UnmarshalAttrs: %v", err)
	}
	if got["extra"].AsString() != "surprise" {
		t.Fatalf("got %#v", got["extra"])
	}
}

func TestUnmarshalValueEmpty(t *testing.T) {
	_, err := UnmarshalValue(nil, cty.String)
	if err == nil {
		t.Fatal("expected error on empty payload")
	}
}
