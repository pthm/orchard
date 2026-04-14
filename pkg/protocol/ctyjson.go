package protocol

import (
	"encoding/json"
	"fmt"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// MarshalValue encodes a cty.Value as a JSON payload suitable for
// embedding in a protocol message. The type is derived from the value
// itself; callers with a declared schema type should use
// MarshalValueAs to pin the on-wire shape.
func MarshalValue(v cty.Value) (json.RawMessage, error) {
	return MarshalValueAs(v, v.Type())
}

// MarshalValueAs encodes v against an explicit target type. This is
// the form the engine should prefer when a schema is available — it
// forces type conformance up front and produces a canonical shape
// (e.g. objects always carry all declared attributes).
func MarshalValueAs(v cty.Value, t cty.Type) (json.RawMessage, error) {
	b, err := ctyjson.Marshal(v, t)
	if err != nil {
		return nil, fmt.Errorf("protocol: marshal cty value: %w", err)
	}
	return json.RawMessage(b), nil
}

// UnmarshalValue decodes a JSON payload into a cty.Value of the given
// type. A null input decodes to cty.NullVal(t). An empty or missing
// payload is an error — callers that want "optional" semantics should
// check for that themselves before calling.
//
// When t is cty.DynamicPseudoType, the payload's cty type is inferred
// from the JSON structure. This is the fallback that lets providers
// declare free-form outputs without losing ergonomics on the wire.
func UnmarshalValue(raw json.RawMessage, t cty.Type) (cty.Value, error) {
	if len(raw) == 0 {
		return cty.NilVal, fmt.Errorf("protocol: unmarshal cty value: empty payload")
	}
	if t == cty.DynamicPseudoType {
		inferred, err := ctyjson.ImpliedType(raw)
		if err != nil {
			return cty.NilVal, fmt.Errorf("protocol: infer cty type: %w", err)
		}
		t = inferred
	}
	v, err := ctyjson.Unmarshal(raw, t)
	if err != nil {
		return cty.NilVal, fmt.Errorf("protocol: unmarshal cty value: %w", err)
	}
	return v, nil
}

// MarshalAttrs is the common-case helper for encoding a map of
// attribute values as it would appear in ExecuteParams.Attrs or
// ExecuteResult.Outputs. Each value is encoded against its own type,
// matching the per-attribute schema model.
func MarshalAttrs(attrs map[string]cty.Value) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(attrs))
	for name, v := range attrs {
		raw, err := MarshalValue(v)
		if err != nil {
			return nil, fmt.Errorf("attr %q: %w", name, err)
		}
		out[name] = raw
	}
	return out, nil
}

// UnmarshalAttrs decodes a map of JSON-encoded values against a
// per-attribute type map. Attributes present in raw but absent from
// types are decoded as cty.DynamicPseudoType, preserving their shape
// for providers that accept free-form input.
func UnmarshalAttrs(raw map[string]json.RawMessage, types map[string]cty.Type) (map[string]cty.Value, error) {
	out := make(map[string]cty.Value, len(raw))
	for name, payload := range raw {
		t, ok := types[name]
		if !ok {
			t = cty.DynamicPseudoType
		}
		v, err := UnmarshalValue(payload, t)
		if err != nil {
			return nil, fmt.Errorf("attr %q: %w", name, err)
		}
		out[name] = v
	}
	return out, nil
}
