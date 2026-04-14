package postgres

import (
	"fmt"
	"math/big"

	"github.com/zclconf/go-cty/cty"
)

// toCtyValue converts a Go value from pgx into a cty.Value suitable
// for flowing through the engine's eval context. pgx returns
// database-native types (string, int32/int64, float64, bool, nil,
// plus pgtype wrappers for less common columns) so this covers the
// common case and falls back to a string representation for types
// we do not yet have a principled mapping for.
//
// This is deliberately permissive rather than strict: a best-effort
// string conversion is a better failure mode during scenario
// development than a hard error on an unhandled type. Columns the
// scenario actually references will surface decoding issues at
// expression-evaluation time, which is a more actionable place to
// see them.
func toCtyValue(v any) cty.Value {
	switch val := v.(type) {
	case nil:
		return cty.NullVal(cty.DynamicPseudoType)
	case string:
		return cty.StringVal(val)
	case bool:
		return cty.BoolVal(val)
	case int:
		return cty.NumberIntVal(int64(val))
	case int32:
		return cty.NumberIntVal(int64(val))
	case int64:
		return cty.NumberIntVal(val)
	case float32:
		return cty.NumberFloatVal(float64(val))
	case float64:
		return cty.NumberFloatVal(val)
	case *big.Float:
		f, _ := val.Float64()
		return cty.NumberFloatVal(f)
	case []byte:
		return cty.StringVal(string(val))
	default:
		return cty.StringVal(fmt.Sprintf("%v", val))
	}
}
