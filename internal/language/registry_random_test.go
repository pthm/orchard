package language

import (
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// callSeeded calls a random function bound to a fixed stream.
func callSeeded(t *testing.T, scope, name string, args ...cty.Value) cty.Value {
	t.Helper()
	v, err := RandomFuncs(NewRand(1, scope))[name].Call(args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return v
}

func num(n int64) cty.Value { return cty.NumberIntVal(n) }

func strs(ss ...string) cty.Value {
	vs := make([]cty.Value, len(ss))
	for i, s := range ss {
		vs[i] = cty.StringVal(s)
	}
	return cty.TupleVal(vs)
}

func TestRandomFunctionsAreSeeded(t *testing.T) {
	for name, args := range map[string][]cty.Value{
		"randint":      {num(0), num(1 << 40)},
		"randfloat":    {num(0), num(1)},
		"randnormal":   {num(0), num(1)},
		"randstring":   {num(16)},
		"randhex":      {num(16)},
		"randshuffle":  {strs("a", "b", "c", "d", "e", "f", "g", "h")},
		"randsample":   {strs("a", "b", "c", "d", "e", "f", "g", "h"), num(3)},
		"uuid":         nil,
		"uuidv7":       {atArg()},
		"ulid":         {atArg()},
		"ksuid":        {atArg()},
		"xid":          {atArg()},
		"objectid":     {atArg()},
		"typeid":       {cty.StringVal("user"), atArg()},
		"nanoid":       nil,
		"randweighted": {cty.MapVal(map[string]cty.Value{"a": num(1), "b": num(1), "c": num(1), "d": num(1)})},
	} {
		a := callSeeded(t, "x", name, args...)
		if b := callSeeded(t, "x", name, args...); !a.RawEquals(b) {
			t.Errorf("%s: same stream gave %#v then %#v", name, a, b)
		}
		differs := false
		for i := range 5 {
			if c := callSeeded(t, "y"+strings.Repeat("y", i), name, args...); !c.RawEquals(a) {
				differs = true
			}
		}
		if !differs {
			t.Errorf("%s: other streams always gave %#v", name, a)
		}
	}
}

func TestRandInt(t *testing.T) {
	r := RandomFuncs(NewRand(1, "x"))["randint"]
	seen := map[int64]bool{}
	for range 200 {
		v, _ := r.Call([]cty.Value{num(-2), num(2)})
		n, _ := v.AsBigFloat().Int64()
		if n < -2 || n > 2 {
			t.Fatalf("randint(-2, 2) = %d", n)
		}
		seen[n] = true
	}
	if len(seen) != 5 {
		t.Errorf("randint(-2, 2) should reach all 5 values, saw %v", seen)
	}
	for _, args := range [][]cty.Value{{num(3), num(1)}, {cty.NumberFloatVal(1.5), num(3)}} {
		if _, err := r.Call(args); err == nil {
			t.Errorf("randint%v should fail", args)
		}
	}
}

func TestRandCollections(t *testing.T) {
	in := strs("a", "b", "c", "d")
	shuffled := callSeeded(t, "x", "randshuffle", in)
	got := make([]string, 0, 4)
	for _, v := range shuffled.AsValueSlice() {
		got = append(got, v.AsString())
	}
	sort.Strings(got)
	if strings.Join(got, "") != "abcd" {
		t.Errorf("randshuffle lost or duplicated elements: %v", got)
	}

	sample := callSeeded(t, "x", "randsample", cty.ListVal(in.AsValueSlice()), num(3))
	if !sample.Type().Equals(cty.List(cty.String)) || sample.LengthInt() != 3 {
		t.Fatalf("randsample of a list should be a list of 3, got %#v", sample)
	}
	if set := cty.SetVal(sample.AsValueSlice()); set.LengthInt() != 3 {
		t.Errorf("randsample repeated an element: %#v", sample)
	}
	if _, err := RandomFuncs(NewRand(1, "x"))["randsample"].Call([]cty.Value{in, num(5)}); err == nil {
		t.Error("randsample of more elements than the list has should fail")
	}

	if c := callSeeded(t, "x", "randchoice", in).AsString(); !strings.ContainsAny(c, "abcd") || len(c) != 1 {
		t.Errorf("randchoice picked %q", c)
	}
	if _, err := RandomFuncs(NewRand(1, "x"))["randchoice"].Call([]cty.Value{cty.EmptyTupleVal}); err == nil {
		t.Error("randchoice of an empty list should fail")
	}
}

func TestRandWeighted(t *testing.T) {
	r := RandomFuncs(NewRand(1, "x"))["randweighted"]
	weights := cty.MapVal(map[string]cty.Value{"common": num(90), "rare": num(10), "never": num(0)})
	counts := map[string]int{}
	for range 2000 {
		v, err := r.Call([]cty.Value{weights})
		if err != nil {
			t.Fatal(err)
		}
		counts[v.AsString()]++
	}
	if counts["never"] != 0 || counts["rare"] < 120 || counts["rare"] > 280 {
		t.Errorf("weights 90/10/0 gave %v", counts)
	}
	if _, err := r.Call([]cty.Value{cty.MapVal(map[string]cty.Value{"a": num(0)})}); err == nil {
		t.Error("all-zero weights should fail")
	}
}

func TestRandStringAndBool(t *testing.T) {
	if s := callSeeded(t, "x", "randstring", num(12), cty.StringVal("ab")).AsString(); len(s) != 12 || strings.Trim(s, "ab") != "" {
		t.Errorf("randstring(12, \"ab\") = %q", s)
	}
	if s := callSeeded(t, "x", "randhex", num(9)).AsString(); len(s) != 9 || strings.Trim(s, "0123456789abcdef") != "" {
		t.Errorf("randhex(9) = %q", s)
	}
	if callSeeded(t, "x", "randbool", num(0)).True() || callSeeded(t, "x", "randbool", num(1)).False() {
		t.Error("randbool(0) must be false and randbool(1) true")
	}
	if _, err := RandomFuncs(NewRand(1, "x"))["randbool"].Call([]cty.Value{num(2)}); err == nil {
		t.Error("randbool(2) should fail")
	}
}
