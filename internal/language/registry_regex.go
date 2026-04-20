package language

import "github.com/zclconf/go-cty/cty/function/stdlib"

func init() {
	register(
		FunctionDef{
			Name: "regex", Category: "regex", Pure: true,
			Description: "Apply a regular expression to a string and return the first match.",
			Params: []ParamDef{
				{Name: "pattern", Type: "string"},
				{Name: "str", Type: "string"},
			},
			ReturnType: "string | list(string)",
			Func:       stdlib.RegexFunc,
		},
		FunctionDef{
			Name: "regexall", Category: "regex", Pure: true,
			Description: "Apply a regular expression to a string and return all matches.",
			Params: []ParamDef{
				{Name: "pattern", Type: "string"},
				{Name: "str", Type: "string"},
			},
			ReturnType: "list(string) | list(list(string))",
			Func:       stdlib.RegexAllFunc,
		},
	)
}
