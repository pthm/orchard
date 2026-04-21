package language

import "github.com/zclconf/go-cty/cty/function/stdlib"

func init() {
	register(
		FunctionDef{
			Name: "upper", Category: "strings", Pure: true,
			Description: "Convert a string to uppercase.",
			Params:      []ParamDef{{Name: "str", Type: "string"}},
			ReturnType:  "string",
			Examples:    []ExampleDef{{Code: `upper("hello")`, Result: `"HELLO"`}},
			Func:        stdlib.UpperFunc,
		},
		FunctionDef{
			Name: "lower", Category: "strings", Pure: true,
			Description: "Convert a string to lowercase.",
			Params:      []ParamDef{{Name: "str", Type: "string"}},
			ReturnType:  "string",
			Examples:    []ExampleDef{{Code: `lower("HELLO")`, Result: `"hello"`}},
			Func:        stdlib.LowerFunc,
		},
		FunctionDef{
			Name: "title", Category: "strings", Pure: true,
			Description: "Convert a string to title case.",
			Params:      []ParamDef{{Name: "str", Type: "string"}},
			ReturnType:  "string",
			Examples:    []ExampleDef{{Code: `title("hello world")`, Result: `"Hello World"`}},
			Func:        stdlib.TitleFunc,
		},
		FunctionDef{
			Name: "trim", Category: "strings", Pure: true,
			Description: "Remove leading and trailing characters from a string.",
			Params: []ParamDef{
				{Name: "str", Type: "string"},
				{Name: "cutset", Type: "string", Description: "Characters to remove."},
			},
			ReturnType: "string",
			Func:       stdlib.TrimFunc,
		},
		FunctionDef{
			Name: "trimspace", Category: "strings", Pure: true,
			Description: "Remove leading and trailing whitespace from a string.",
			Params:      []ParamDef{{Name: "str", Type: "string"}},
			ReturnType:  "string",
			Func:        stdlib.TrimSpaceFunc,
		},
		FunctionDef{
			Name: "trimprefix", Category: "strings", Pure: true,
			Description: "Remove a prefix from the start of a string.",
			Params: []ParamDef{
				{Name: "str", Type: "string"},
				{Name: "prefix", Type: "string"},
			},
			ReturnType: "string",
			Func:       stdlib.TrimPrefixFunc,
		},
		FunctionDef{
			Name: "trimsuffix", Category: "strings", Pure: true,
			Description: "Remove a suffix from the end of a string.",
			Params: []ParamDef{
				{Name: "str", Type: "string"},
				{Name: "suffix", Type: "string"},
			},
			ReturnType: "string",
			Func:       stdlib.TrimSuffixFunc,
		},
		FunctionDef{
			Name: "substr", Category: "strings", Pure: true,
			Description: "Extract a substring by offset and length.",
			Params: []ParamDef{
				{Name: "str", Type: "string"},
				{Name: "offset", Type: "number"},
				{Name: "length", Type: "number"},
			},
			ReturnType: "string",
			Func:       stdlib.SubstrFunc,
		},
		FunctionDef{
			Name: "replace", Category: "strings", Pure: true,
			Description: "Replace all occurrences of a substring.",
			Params: []ParamDef{
				{Name: "str", Type: "string"},
				{Name: "old", Type: "string"},
				{Name: "new", Type: "string"},
			},
			ReturnType: "string",
			Func:       stdlib.ReplaceFunc,
		},
		FunctionDef{
			Name: "format", Category: "strings", Pure: true,
			Description: "Format a string using printf-style verbs.",
			Params:      []ParamDef{{Name: "format", Type: "string"}},
			VarParam:    &ParamDef{Name: "args", Type: "any"},
			ReturnType:  "string",
			Examples:    []ExampleDef{{Code: `format("hello %s, you are %d", "alice", 30)`, Result: `"hello alice, you are 30"`}},
			Func:        stdlib.FormatFunc,
		},
		FunctionDef{
			Name: "formatlist", Category: "strings", Pure: true,
			Description: "Format each element of a list using printf-style verbs.",
			Params:      []ParamDef{{Name: "format", Type: "string"}},
			VarParam:    &ParamDef{Name: "args", Type: "any"},
			ReturnType:  "list(string)",
			Func:        stdlib.FormatListFunc,
		},
		FunctionDef{
			Name: "split", Category: "strings", Pure: true,
			Description: "Split a string by a separator.",
			Params: []ParamDef{
				{Name: "separator", Type: "string"},
				{Name: "str", Type: "string"},
			},
			ReturnType: "list(string)",
			Examples:   []ExampleDef{{Code: `split(",", "a,b,c")`, Result: `["a", "b", "c"]`}},
			Func:       stdlib.SplitFunc,
		},
		FunctionDef{
			Name: "join", Category: "strings", Pure: true,
			Description: "Join a list of strings with a separator.",
			Params: []ParamDef{
				{Name: "separator", Type: "string"},
				{Name: "list", Type: "list(string)"},
			},
			ReturnType: "string",
			Examples:   []ExampleDef{{Code: `join(", ", ["a", "b", "c"])`, Result: `"a, b, c"`}},
			Func:       stdlib.JoinFunc,
		},
		FunctionDef{
			Name: "chomp", Category: "strings", Pure: true,
			Description: "Remove trailing newlines from a string.",
			Params:      []ParamDef{{Name: "str", Type: "string"}},
			ReturnType:  "string",
			Func:        stdlib.ChompFunc,
		},
		FunctionDef{
			Name: "strlen", Category: "strings", Pure: true,
			Description: "Return the length of a string in characters.",
			Detail:      "Use `length` for collections (lists, maps, tuples). `strlen` is for strings only.",
			Params:      []ParamDef{{Name: "str", Type: "string"}},
			ReturnType:  "number",
			Func:        stdlib.StrlenFunc,
		},
	)
}
