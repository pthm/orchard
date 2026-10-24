package sqlrow

import (
	"fmt"
	"math/big"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// Params returns a query action's params attribute as driver
// arguments, converting each with arg. No params, null or an empty
// list returns nil, so the query runs as it would without them.
func Params(attrs map[string]cty.Value, arg func(cty.Value) (any, error)) ([]any, error) {
	v, ok := attrs["params"]
	if !ok || v.IsNull() {
		return nil, nil
	}
	t := v.Type()
	if !t.IsListType() && !t.IsTupleType() {
		return nil, fmt.Errorf("params must be a list, got %s", t.FriendlyName())
	}
	elems := v.AsValueSlice()
	if len(elems) == 0 {
		return nil, nil
	}
	out := make([]any, len(elems))
	for i, e := range elems {
		a, err := arg(e)
		if err != nil {
			return nil, fmt.Errorf("params[%d]: %w", i, err)
		}
		out[i] = a
	}
	return out, nil
}

// Arg converts one parameter: null as NULL, strings and bools as
// themselves, whole numbers as int64 and other numbers as exact
// decimal text, and lists and objects as JSON text.
func Arg(v cty.Value) (any, error) {
	if !v.IsKnown() {
		return nil, fmt.Errorf("value isn't known")
	}
	if v.IsNull() {
		return nil, nil
	}
	switch v.Type() {
	case cty.String:
		return v.AsString(), nil
	case cty.Bool:
		return v.True(), nil
	case cty.Number:
		bf := v.AsBigFloat()
		if i, acc := bf.Int64(); acc == big.Exact {
			return i, nil
		}
		return bf.Text('f', -1), nil
	}
	b, err := ctyjson.Marshal(v, v.Type())
	if err != nil {
		return nil, err
	}
	return string(b), nil
}
