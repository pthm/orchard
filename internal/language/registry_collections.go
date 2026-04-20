package language

import "github.com/zclconf/go-cty/cty/function/stdlib"

func init() {
	register(
		FunctionDef{
			Name: "concat", Category: "collections", Pure: true,
			Description: "Concatenate two or more lists into one.",
			VarParam:    &ParamDef{Name: "lists", Type: "list"},
			ReturnType:  "list",
			Examples:    []ExampleDef{{Code: `concat(["a"], ["b", "c"])`, Result: `["a", "b", "c"]`}},
			Func:        stdlib.ConcatFunc,
		},
		FunctionDef{
			Name: "length", Category: "collections", Pure: true,
			Description: "Return the number of elements in a collection.",
			Detail:      "Works on lists, maps, and tuples. Use `strlen` for string length.",
			Params:      []ParamDef{{Name: "collection", Type: "list | map | tuple"}},
			ReturnType:  "number",
			Func:        stdlib.LengthFunc,
		},
		FunctionDef{
			Name: "keys", Category: "collections", Pure: true,
			Description: "Return the keys of a map as a list.",
			Params:      []ParamDef{{Name: "map", Type: "map"}},
			ReturnType:  "list(string)",
			Func:        stdlib.KeysFunc,
		},
		FunctionDef{
			Name: "values", Category: "collections", Pure: true,
			Description: "Return the values of a map as a list.",
			Params:      []ParamDef{{Name: "map", Type: "map"}},
			ReturnType:  "list",
			Func:        stdlib.ValuesFunc,
		},
		FunctionDef{
			Name: "lookup", Category: "collections", Pure: true,
			Description: "Look up a key in a map with a default fallback.",
			Params: []ParamDef{
				{Name: "map", Type: "map"},
				{Name: "key", Type: "string"},
				{Name: "default", Type: "any"},
			},
			ReturnType: "any",
			Func:       stdlib.LookupFunc,
		},
		FunctionDef{
			Name: "contains", Category: "collections", Pure: true,
			Description: "Check whether a list or set contains a given value.",
			Params: []ParamDef{
				{Name: "list", Type: "list"},
				{Name: "value", Type: "any"},
			},
			ReturnType: "bool",
			Func:       stdlib.ContainsFunc,
		},
		FunctionDef{
			Name: "merge", Category: "collections", Pure: true,
			Description: "Merge two or more maps. Later keys override earlier ones.",
			VarParam:   &ParamDef{Name: "maps", Type: "map"},
			ReturnType: "map",
			Func:       stdlib.MergeFunc,
		},
		FunctionDef{
			Name: "reverse", Category: "collections", Pure: true,
			Description: "Reverse the order of elements in a list.",
			Params:      []ParamDef{{Name: "list", Type: "list"}},
			ReturnType:  "list",
			Func:        stdlib.ReverseListFunc,
		},
		FunctionDef{
			Name: "sort", Category: "collections", Pure: true,
			Description: "Sort a list of strings lexicographically.",
			Params:      []ParamDef{{Name: "list", Type: "list(string)"}},
			ReturnType:  "list(string)",
			Func:        stdlib.SortFunc,
		},
		FunctionDef{
			Name: "slice", Category: "collections", Pure: true,
			Description: "Extract a contiguous sub-list.",
			Params: []ParamDef{
				{Name: "list", Type: "list"},
				{Name: "start", Type: "number"},
				{Name: "end", Type: "number"},
			},
			ReturnType: "list",
			Func:       stdlib.SliceFunc,
		},
		FunctionDef{
			Name: "distinct", Category: "collections", Pure: true,
			Description: "Remove duplicate elements from a list.",
			Params:      []ParamDef{{Name: "list", Type: "list"}},
			ReturnType:  "list",
			Func:        stdlib.DistinctFunc,
		},
		FunctionDef{
			Name: "element", Category: "collections", Pure: true,
			Description: "Return the element at the given index in a list.",
			Params: []ParamDef{
				{Name: "list", Type: "list"},
				{Name: "index", Type: "number"},
			},
			ReturnType: "any",
			Func:       stdlib.ElementFunc,
		},
		FunctionDef{
			Name: "range", Category: "collections", Pure: true,
			Description: "Generate a list of numbers.",
			VarParam:    &ParamDef{Name: "args", Type: "number"},
			ReturnType:  "list(number)",
			Examples:    []ExampleDef{{Code: `range(3)`, Result: `[0, 1, 2]`}},
			Func:        stdlib.RangeFunc,
		},
		FunctionDef{
			Name: "zipmap", Category: "collections", Pure: true,
			Description: "Construct a map from a list of keys and a list of values.",
			Params: []ParamDef{
				{Name: "keys", Type: "list(string)"},
				{Name: "values", Type: "list"},
			},
			ReturnType: "map",
			Func:       stdlib.ZipmapFunc,
		},
	)
}
