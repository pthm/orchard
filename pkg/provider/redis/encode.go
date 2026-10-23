package redis

import (
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// encodeValue turns a cty value into the string Redis stores: strings
// as-is, numbers in full-precision decimal, bools as true/false, and
// collections as JSON. Null isn't a value Redis can hold.
func encodeValue(v cty.Value) (string, error) {
	if !v.IsKnown() {
		return "", fmt.Errorf("value isn't known")
	}
	if v.IsNull() {
		return "", fmt.Errorf("value is null; delete the key with redis_command instead")
	}
	switch ty := v.Type(); ty {
	case cty.String:
		return v.AsString(), nil
	case cty.Number:
		return v.AsBigFloat().Text('f', -1), nil
	case cty.Bool:
		if v.True() {
			return "true", nil
		}
		return "false", nil
	default:
		b, err := ctyjson.Marshal(v, ty)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
}

// commandArg converts one redis_command argument, which must be a
// string, number or bool.
func commandArg(v cty.Value) (string, error) {
	if !v.IsKnown() || v.IsNull() {
		return "", fmt.Errorf("arguments must be known, non-null values")
	}
	switch v.Type() {
	case cty.String, cty.Number, cty.Bool:
		return encodeValue(v)
	default:
		return "", fmt.Errorf("arguments must be strings, numbers or bools; got %s (jsonencode it first)", v.Type().FriendlyName())
	}
}

// parseTTL parses a ttl attribute: a positive Go duration.
func parseTTL(v cty.Value) (time.Duration, bool, error) {
	if !v.IsKnown() || v.IsNull() {
		return 0, false, nil
	}
	d, err := time.ParseDuration(v.AsString())
	if err != nil {
		return 0, false, fmt.Errorf("ttl %q isn't a duration like \"30s\" or \"1h\"", v.AsString())
	}
	if d <= 0 {
		return 0, false, fmt.Errorf("ttl must be positive, got %q", v.AsString())
	}
	return d, true, nil
}

// fromReply converts a go-redis reply (RESP2 or RESP3) into cty: bulk
// and simple strings to strings, integers, doubles and big numbers to
// numbers, booleans to bools, arrays and sets to tuples, maps to
// objects with string keys, and nil to null.
func fromReply(r any) cty.Value {
	switch v := r.(type) {
	case nil:
		return cty.NullVal(cty.DynamicPseudoType)
	case string:
		return cty.StringVal(v)
	case []byte:
		return cty.StringVal(string(v))
	case int64:
		return cty.NumberIntVal(v)
	case float64:
		return cty.NumberFloatVal(v)
	case bool:
		return cty.BoolVal(v)
	case *big.Int:
		return cty.NumberVal(new(big.Float).SetInt(v))
	case []any:
		if len(v) == 0 {
			return cty.EmptyTupleVal
		}
		out := make([]cty.Value, len(v))
		for i, e := range v {
			out[i] = fromReply(e)
		}
		return cty.TupleVal(out)
	case map[any]any:
		if len(v) == 0 {
			return cty.EmptyObjectVal
		}
		out := make(map[string]cty.Value, len(v))
		for k, e := range v {
			out[fmt.Sprint(k)] = fromReply(e)
		}
		return cty.ObjectVal(out)
	case map[string]string:
		if len(v) == 0 {
			return cty.EmptyObjectVal
		}
		out := make(map[string]cty.Value, len(v))
		for k, e := range v {
			out[k] = cty.StringVal(e)
		}
		return cty.ObjectVal(out)
	default:
		return cty.StringVal(fmt.Sprint(v))
	}
}

// stringList is a sorted cty list of strings (empty, not null, when
// there are none, to match the declared list(string) output).
func stringList(items []string) cty.Value {
	if len(items) == 0 {
		return cty.ListValEmpty(cty.String)
	}
	sort.Strings(items)
	vals := make([]cty.Value, len(items))
	for i, s := range items {
		vals[i] = cty.StringVal(s)
	}
	return cty.ListVal(vals)
}
