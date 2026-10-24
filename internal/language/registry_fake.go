package language

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/pthm/orchard/internal/suggest"
)

func init() {
	register(
		FunctionDef{
			Name: "fake", Category: "fake",
			Description: "Generate fake data of the given kind: a name, email, address, company, sentence, and 350 more.",
			Detail: "Kinds come from [gofakeit](https://github.com/brianvoe/gofakeit); the [fake data kinds](../fake-kinds/) " +
				"page lists them with their parameters. Pass parameters as an object; a list sets a parameter that takes " +
				"several values. Kinds that produce a number, bool or string return that type; the rest (`person`, `address`, " +
				"`creditcard`, ...) return an object. An unknown kind or parameter fails at plan time.",
			Params:     []ParamDef{{Name: "kind", Type: "string", Description: "What to generate, e.g. `\"email\"`."}},
			VarParam:   &ParamDef{Name: "params", Type: "object", Description: "The kind's parameters. Optional."},
			ReturnType: "any",
			Examples: []ExampleDef{
				{Code: `fake("name")`, Result: `"Giovanni Bartell"`},
				{Code: `fake("number", { min = 18, max = 90 })`, Result: `42`},
				{Code: `fake("loremipsumsentence", { wordcount = 6 })`, Result: `"Illum ab temporibus exercitationem et ut."`},
				{Code: `fake("address").city`, Result: `"Philadelphia"`},
			},
			Random: fakeFunc,
		},
		FunctionDef{
			Name: "faketemplate", Category: "fake",
			Description: "Fill a pattern with fake data: `{kind}` placeholders, `#` for a digit, `?` for a letter.",
			Detail:      "A placeholder can pass parameters after a colon, in order: `{number:1,100}`.",
			Params:      []ParamDef{{Name: "pattern", Type: "string"}},
			ReturnType:  "string",
			Examples: []ExampleDef{
				{Code: `faketemplate("{firstname}.{lastname}@example.com")`, Result: `"Fabian.Weber@example.com"`},
				{Code: `faketemplate("INV-####-??")`, Result: `"INV-4821-qk"`},
			},
			Random: fakeTemplateFunc,
		},
	)
}

// FakeKind is one kind fake() accepts.
type FakeKind struct {
	Name string
	gofakeit.Info
}

// FakeKinds returns gofakeit's generators sorted by name, for docs.
func FakeKinds() []FakeKind {
	names := fakeNames()
	out := make([]FakeKind, len(names))
	for i, n := range names {
		out[i] = FakeKind{n, *gofakeit.GetFuncLookup(n)}
	}
	return out
}

func fakeNames() []string {
	names := make([]string, 0, len(gofakeit.FuncLookups))
	for n := range gofakeit.FuncLookups {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// fakeKind resolves kind and checks params' names against it.
func fakeKind(kind string, params cty.Value) (*gofakeit.Info, error) {
	info := gofakeit.GetFuncLookup(kind)
	if info == nil {
		return nil, fmt.Errorf("no fake data kind %q%s", kind, suggest.Hint(kind, fakeNames()))
	}
	if params == cty.NilVal || !params.IsKnown() || params.IsNull() {
		return info, nil
	}
	t := params.Type()
	if !t.IsObjectType() && !t.IsMapType() {
		return nil, fmt.Errorf("fake: params must be an object, got %s", t.FriendlyName())
	}
	fields := make([]string, len(info.Params))
	for i, p := range info.Params {
		fields[i] = p.Field
	}
	for it := params.ElementIterator(); it.Next(); {
		k, _ := it.Element()
		name := k.AsString()
		if !containsString(fields, name) {
			if len(fields) == 0 {
				return nil, fmt.Errorf("fake: %q takes no parameters; got %q", kind, name)
			}
			return nil, fmt.Errorf("fake: %q has no parameter %q%s; it takes %s", kind, name, suggest.Hint(name, fields), strings.Join(fields, ", "))
		}
	}
	return info, nil
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// fakeType is the cty type a kind's output converts to.
func fakeType(output string) cty.Type {
	switch output {
	case "string", "time", "[]byte":
		return cty.String
	case "bool":
		return cty.Bool
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float", "float32", "float64":
		return cty.Number
	case "[]string":
		return cty.List(cty.String)
	case "[]int", "[]uint":
		return cty.List(cty.Number)
	}
	return cty.DynamicPseudoType
}

func fakeFunc(r *Rand) function.Function {
	return function.New(&function.Spec{
		Params:   []function.Parameter{{Name: "kind", Type: cty.String}},
		VarParam: &function.Parameter{Name: "params", Type: cty.DynamicPseudoType},
		Type: func(args []cty.Value) (cty.Type, error) {
			if len(args) > 2 {
				return cty.NilType, fmt.Errorf("takes a kind and at most one params object")
			}
			if !args[0].IsKnown() {
				return cty.DynamicPseudoType, nil
			}
			params := cty.NilVal
			if len(args) == 2 {
				params = args[1]
			}
			info, err := fakeKind(args[0].AsString(), params)
			if err != nil {
				return cty.NilType, err
			}
			return fakeType(info.Output), nil
		},
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			kind := args[0].AsString()
			params := cty.NilVal
			if len(args) == 2 {
				params = args[1]
			}
			info, err := fakeKind(kind, params)
			if err != nil {
				return cty.NilVal, err
			}
			mp := gofakeit.NewMapParams()
			if params != cty.NilVal && !params.IsNull() {
				for it := params.ElementIterator(); it.Next(); {
					k, v := it.Element()
					vals, err := paramStrings(v)
					if err != nil {
						return cty.NilVal, fmt.Errorf("fake: %s: %w", k.AsString(), err)
					}
					for _, s := range vals {
						mp.Add(k.AsString(), s)
					}
				}
			}
			out, err := info.Generate(r.fake, mp, info)
			if err != nil {
				return cty.NilVal, fmt.Errorf("fake %q: %w", kind, err)
			}
			return fakeValue(out, retType)
		},
	})
}

// paramStrings renders a parameter value as gofakeit's strings.
func paramStrings(v cty.Value) ([]string, error) {
	if v.IsNull() {
		return nil, nil
	}
	t := v.Type()
	switch {
	case t == cty.String:
		return []string{v.AsString()}, nil
	case t == cty.Number:
		return []string{v.AsBigFloat().Text('f', -1)}, nil
	case t == cty.Bool:
		if v.True() {
			return []string{"true"}, nil
		}
		return []string{"false"}, nil
	case t.IsListType() || t.IsTupleType() || t.IsSetType():
		var out []string
		for _, e := range v.AsValueSlice() {
			s, err := paramStrings(e)
			if err != nil {
				return nil, err
			}
			out = append(out, s...)
		}
		return out, nil
	}
	return nil, fmt.Errorf("must be a string, number, bool or list of them, got %s", t.FriendlyName())
}

// fakeValue converts a generator's Go result to cty.
func fakeValue(out any, retType cty.Type) (cty.Value, error) {
	switch v := out.(type) {
	case string:
		return cty.StringVal(v), nil
	case []byte:
		return cty.StringVal(string(v)), nil
	case time.Time:
		return cty.StringVal(v.UTC().Format(time.RFC3339)), nil
	case bool:
		return cty.BoolVal(v), nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return cty.NilVal, fmt.Errorf("fake: encode %T: %w", out, err)
	}
	t := retType
	if t == cty.DynamicPseudoType {
		if t, err = ctyjson.ImpliedType(b); err != nil {
			return cty.NilVal, fmt.Errorf("fake: %w", err)
		}
	}
	return ctyjson.Unmarshal(b, t)
}

func fakeTemplateFunc(r *Rand) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "pattern", Type: cty.String}},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			s, err := r.fake.Generate(args[0].AsString())
			if err != nil {
				return cty.NilVal, fmt.Errorf("faketemplate: %w", err)
			}
			return cty.StringVal(s), nil
		},
	})
}
