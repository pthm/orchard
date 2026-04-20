package language

import "github.com/zclconf/go-cty/cty/function/stdlib"

func init() {
	register(
		FunctionDef{
			Name: "setintersection", Category: "sets", Pure: true,
			Description: "Return elements common to all given sets.",
			VarParam:    &ParamDef{Name: "sets", Type: "set"},
			ReturnType:  "set",
			Func:        stdlib.SetIntersectionFunc,
		},
		FunctionDef{
			Name: "setproduct", Category: "sets", Pure: true,
			Description: "Compute the Cartesian product of the given sets.",
			VarParam:    &ParamDef{Name: "sets", Type: "set"},
			ReturnType:  "set(tuple)",
			Func:        stdlib.SetProductFunc,
		},
		FunctionDef{
			Name: "setsubtract", Category: "sets", Pure: true,
			Description: "Return elements in the first set that are not in the second.",
			Params: []ParamDef{
				{Name: "a", Type: "set"},
				{Name: "b", Type: "set"},
			},
			ReturnType: "set",
			Func:       stdlib.SetSubtractFunc,
		},
		FunctionDef{
			Name: "setunion", Category: "sets", Pure: true,
			Description: "Return the union of all given sets.",
			VarParam:    &ParamDef{Name: "sets", Type: "set"},
			ReturnType:  "set",
			Func:        stdlib.SetUnionFunc,
		},
	)
}
