package language

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

func init() {
	register(
		FunctionDef{
			Name: "randint", Category: "random",
			Description: "Return a random integer between min and max, inclusive.",
			Params: []ParamDef{
				{Name: "min", Type: "number", Description: "Smallest value, an integer."},
				{Name: "max", Type: "number", Description: "Largest value, an integer."},
			},
			ReturnType: "number",
			Examples:   []ExampleDef{{Code: `randint(1, 6)`, Result: `4`}},
			Random:     randIntFunc,
		},
		FunctionDef{
			Name: "randfloat", Category: "random",
			Description: "Return a random number at least min and below max.",
			Params: []ParamDef{
				{Name: "min", Type: "number"},
				{Name: "max", Type: "number"},
			},
			ReturnType: "number",
			Examples:   []ExampleDef{{Code: `floor(randfloat(5, 500) * 100) / 100`, Result: `123.45`}},
			Random:     randFloatFunc,
		},
		FunctionDef{
			Name: "randnormal", Category: "random",
			Description: "Return a random number from a normal distribution.",
			Detail:      "Most values land within one `stddev` of `mean`; use `max`/`min` to clamp the tails.",
			Params: []ParamDef{
				{Name: "mean", Type: "number"},
				{Name: "stddev", Type: "number"},
			},
			ReturnType: "number",
			Examples:   []ExampleDef{{Code: `round(randnormal(40, 10))`}},
			Random:     randNormalFunc,
		},
		FunctionDef{
			Name: "randbool", Category: "random",
			Description: "Return true with the given probability (default 0.5).",
			VarParam:    &ParamDef{Name: "probability", Type: "number", Description: "Chance of true, from 0 to 1. Optional."},
			ReturnType:  "bool",
			Examples:    []ExampleDef{{Code: `randbool(0.1)`, Result: `false`}},
			Random:      randBoolFunc,
		},
		FunctionDef{
			Name: "randchoice", Category: "random",
			Description: "Return one element of a list, picked at random.",
			Params:      []ParamDef{{Name: "list", Type: "list"}},
			ReturnType:  "any",
			Examples:    []ExampleDef{{Code: `randchoice(["eu", "us", "apac"])`, Result: `"us"`}},
			Random:      randChoiceFunc,
		},
		FunctionDef{
			Name: "randweighted", Category: "random",
			Description: "Return a key of a map, picked with probability proportional to its weight.",
			Params:      []ParamDef{{Name: "weights", Type: "map(number)", Description: "Keys to choose from, each with a non-negative weight."}},
			ReturnType:  "string",
			Examples:    []ExampleDef{{Code: `randweighted({ card = 70, paypal = 20, bank = 10 })`, Result: `"card"`}},
			Random:      randWeightedFunc,
		},
		FunctionDef{
			Name: "randsample", Category: "random",
			Description: "Return n distinct elements of a list, in random order.",
			Params: []ParamDef{
				{Name: "list", Type: "list"},
				{Name: "n", Type: "number", Description: "How many elements, at most the list's length."},
			},
			ReturnType: "list",
			Examples:   []ExampleDef{{Code: `randsample(["a", "b", "c", "d"], 2)`, Result: `["c", "a"]`}},
			Random:     randSampleFunc,
		},
		FunctionDef{
			Name: "randshuffle", Category: "random",
			Description: "Return a list's elements in random order.",
			Params:      []ParamDef{{Name: "list", Type: "list"}},
			ReturnType:  "list",
			Examples:    []ExampleDef{{Code: `randshuffle([1, 2, 3])`, Result: `[3, 1, 2]`}},
			Random:      randShuffleFunc,
		},
		FunctionDef{
			Name: "randstring", Category: "random",
			Description: "Return a random string of the given length.",
			Detail:      "Characters come from `charset`, letters and digits by default.",
			Params:      []ParamDef{{Name: "length", Type: "number"}},
			VarParam:    &ParamDef{Name: "charset", Type: "string", Description: "Characters to draw from. Optional."},
			ReturnType:  "string",
			Examples: []ExampleDef{
				{Code: `randstring(8)`, Result: `"q3ZxT0bA"`},
				{Code: `randstring(6, "0123456789")`, Result: `"402913"`},
			},
			Random: randStringFunc,
		},
		FunctionDef{
			Name: "randhex", Category: "random",
			Description: "Return a random lowercase hex string of the given length.",
			Params:      []ParamDef{{Name: "length", Type: "number"}},
			ReturnType:  "string",
			Examples:    []ExampleDef{{Code: `randhex(12)`, Result: `"9f04c1a77be2"`}},
			Random:      func(r *Rand) function.Function { return randCharsetFunc(r, "0123456789abcdef") },
		},
	)
}

// intArg returns a whole-number argument as an int64.
func intArg(fn, name string, v cty.Value) (int64, error) {
	bf := v.AsBigFloat()
	n, acc := bf.Int64()
	if acc != big.Exact {
		return 0, fmt.Errorf("%s: %s must be a whole number, got %s", fn, name, bf.Text('f', -1))
	}
	return n, nil
}

func randIntFunc(r *Rand) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "min", Type: cty.Number}, {Name: "max", Type: cty.Number}},
		Type:   function.StaticReturnType(cty.Number),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			lo, err := intArg("randint", "min", args[0])
			if err != nil {
				return cty.NilVal, err
			}
			hi, err := intArg("randint", "max", args[1])
			if err != nil {
				return cty.NilVal, err
			}
			if lo > hi {
				return cty.NilVal, fmt.Errorf("randint: min (%d) is greater than max (%d)", lo, hi)
			}
			// ponytail: spans past int64 (full-range min/max) overflow; nobody seeds data that wide.
			return cty.NumberIntVal(lo + r.Int64N(hi-lo+1)), nil
		},
	})
}

func randFloatFunc(r *Rand) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "min", Type: cty.Number}, {Name: "max", Type: cty.Number}},
		Type:   function.StaticReturnType(cty.Number),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			lo, _ := args[0].AsBigFloat().Float64()
			hi, _ := args[1].AsBigFloat().Float64()
			if lo > hi {
				return cty.NilVal, fmt.Errorf("randfloat: min (%g) is greater than max (%g)", lo, hi)
			}
			return cty.NumberFloatVal(lo + r.Float64()*(hi-lo)), nil
		},
	})
}

func randNormalFunc(r *Rand) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "mean", Type: cty.Number}, {Name: "stddev", Type: cty.Number}},
		Type:   function.StaticReturnType(cty.Number),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			mean, _ := args[0].AsBigFloat().Float64()
			sd, _ := args[1].AsBigFloat().Float64()
			if sd < 0 {
				return cty.NilVal, fmt.Errorf("randnormal: stddev must not be negative, got %g", sd)
			}
			return cty.NumberFloatVal(mean + r.NormFloat64()*sd), nil
		},
	})
}

func randBoolFunc(r *Rand) function.Function {
	return function.New(&function.Spec{
		VarParam: &function.Parameter{Name: "probability", Type: cty.Number},
		Type:     function.StaticReturnType(cty.Bool),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			p := 0.5
			switch len(args) {
			case 0:
			case 1:
				p, _ = args[0].AsBigFloat().Float64()
				if p < 0 || p > 1 {
					return cty.NilVal, fmt.Errorf("randbool: probability must be between 0 and 1, got %g", p)
				}
			default:
				return cty.NilVal, fmt.Errorf("randbool: takes at most one argument, got %d", len(args))
			}
			return cty.BoolVal(r.Float64() < p), nil
		},
	})
}

// elements returns the elements of a list, tuple or set argument.
func elements(fn string, v cty.Value) ([]cty.Value, error) {
	t := v.Type()
	if !t.IsListType() && !t.IsTupleType() && !t.IsSetType() {
		return nil, fmt.Errorf("%s: expected a list, got %s", fn, t.FriendlyName())
	}
	return v.AsValueSlice(), nil
}

// sameKind builds a collection like in from elems: a tuple stays a
// tuple; lists and sets become lists.
func sameKind(in cty.Value, elems []cty.Value) cty.Value {
	t := in.Type()
	if t.IsTupleType() {
		return cty.TupleVal(elems)
	}
	if len(elems) == 0 {
		return cty.ListValEmpty(t.ElementType())
	}
	return cty.ListVal(elems)
}

// listResult is the Type of a function that returns a reordering or
// subset of its list argument.
func listResult(args []cty.Value) (cty.Type, error) {
	t := args[0].Type()
	switch {
	case t.IsListType() || t.IsSetType():
		return cty.List(t.ElementType()), nil
	case t.IsTupleType() || t == cty.DynamicPseudoType:
		// A subset of a tuple's elements has a type that depends on
		// which ones are picked.
		return cty.DynamicPseudoType, nil
	}
	return cty.NilType, fmt.Errorf("expected a list, got %s", t.FriendlyName())
}

func randChoiceFunc(r *Rand) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "list", Type: cty.DynamicPseudoType}},
		Type: func(args []cty.Value) (cty.Type, error) {
			t := args[0].Type()
			if t.IsListType() || t.IsSetType() {
				return t.ElementType(), nil
			}
			return cty.DynamicPseudoType, nil
		},
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			elems, err := elements("randchoice", args[0])
			if err != nil {
				return cty.NilVal, err
			}
			if len(elems) == 0 {
				return cty.NilVal, fmt.Errorf("randchoice: the list is empty")
			}
			return elems[r.IntN(len(elems))], nil
		},
	})
}

func randWeightedFunc(r *Rand) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "weights", Type: cty.Map(cty.Number)}},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			m := args[0].AsValueMap()
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			// Sorted so the same seed picks the same key.
			sort.Strings(keys)
			weights := make([]float64, len(keys))
			total := 0.0
			for i, k := range keys {
				w, _ := m[k].AsBigFloat().Float64()
				if w < 0 || math.IsInf(w, 0) {
					return cty.NilVal, fmt.Errorf("randweighted: weight for %q must be a non-negative number, got %g", k, w)
				}
				weights[i] = w
				total += w
			}
			if total == 0 {
				return cty.NilVal, fmt.Errorf("randweighted: needs at least one key with a positive weight")
			}
			x := r.Float64() * total
			for i, w := range weights {
				if x < w {
					return cty.StringVal(keys[i]), nil
				}
				x -= w
			}
			// Float rounding can leave x just past the last bucket.
			for i := len(keys) - 1; ; i-- {
				if weights[i] > 0 {
					return cty.StringVal(keys[i]), nil
				}
			}
		},
	})
}

func randSampleFunc(r *Rand) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "list", Type: cty.DynamicPseudoType}, {Name: "n", Type: cty.Number}},
		Type:   listResult,
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			elems, err := elements("randsample", args[0])
			if err != nil {
				return cty.NilVal, err
			}
			n, err := intArg("randsample", "n", args[1])
			if err != nil {
				return cty.NilVal, err
			}
			if n < 0 || n > int64(len(elems)) {
				return cty.NilVal, fmt.Errorf("randsample: n must be between 0 and the list's length (%d), got %d", len(elems), n)
			}
			out := append([]cty.Value(nil), elems...)
			r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
			return sameKind(args[0], out[:n]), nil
		},
	})
}

func randShuffleFunc(r *Rand) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "list", Type: cty.DynamicPseudoType}},
		Type:   listResult,
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			elems, err := elements("randshuffle", args[0])
			if err != nil {
				return cty.NilVal, err
			}
			out := append([]cty.Value(nil), elems...)
			r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
			return sameKind(args[0], out), nil
		},
	})
}

const alphanumeric = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func randStringFunc(r *Rand) function.Function {
	return function.New(&function.Spec{
		Params:   []function.Parameter{{Name: "length", Type: cty.Number}},
		VarParam: &function.Parameter{Name: "charset", Type: cty.String},
		Type:     function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			charset := alphanumeric
			switch len(args) {
			case 1:
			case 2:
				charset = args[1].AsString()
			default:
				return cty.NilVal, fmt.Errorf("randstring: takes a length and at most one charset")
			}
			return randChars(r, "randstring", args[0], charset)
		},
	})
}

func randCharsetFunc(r *Rand, charset string) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "length", Type: cty.Number}},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			return randChars(r, "randhex", args[0], charset)
		},
	})
}

func randChars(r *Rand, fn string, length cty.Value, charset string) (cty.Value, error) {
	n, err := intArg(fn, "length", length)
	if err != nil {
		return cty.NilVal, err
	}
	if n < 0 || n > 1<<20 {
		return cty.NilVal, fmt.Errorf("%s: length must be between 0 and 1048576, got %d", fn, n)
	}
	chars := []rune(charset)
	if len(chars) == 0 || !utf8.ValidString(charset) {
		return cty.NilVal, fmt.Errorf("%s: charset must be a non-empty string", fn)
	}
	var b strings.Builder
	for range n {
		b.WriteRune(chars[r.IntN(len(chars))])
	}
	return cty.StringVal(b.String()), nil
}
