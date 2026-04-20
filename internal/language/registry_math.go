package language

import "github.com/zclconf/go-cty/cty/function/stdlib"

func init() {
	register(
		FunctionDef{
			Name: "abs", Category: "math", Pure: true,
			Description: "Return the absolute value of a number.",
			Params:      []ParamDef{{Name: "n", Type: "number"}},
			ReturnType:  "number",
			Func:        stdlib.AbsoluteFunc,
		},
		FunctionDef{
			Name: "ceil", Category: "math", Pure: true,
			Description: "Round a number up to the nearest integer.",
			Params:      []ParamDef{{Name: "n", Type: "number"}},
			ReturnType:  "number",
			Func:        stdlib.CeilFunc,
		},
		FunctionDef{
			Name: "floor", Category: "math", Pure: true,
			Description: "Round a number down to the nearest integer.",
			Params:      []ParamDef{{Name: "n", Type: "number"}},
			ReturnType:  "number",
			Func:        stdlib.FloorFunc,
		},
		FunctionDef{
			Name: "max", Category: "math", Pure: true,
			Description: "Return the largest of the given numbers.",
			VarParam:    &ParamDef{Name: "numbers", Type: "number"},
			ReturnType:  "number",
			Func:        stdlib.MaxFunc,
		},
		FunctionDef{
			Name: "min", Category: "math", Pure: true,
			Description: "Return the smallest of the given numbers.",
			VarParam:    &ParamDef{Name: "numbers", Type: "number"},
			ReturnType:  "number",
			Func:        stdlib.MinFunc,
		},
		FunctionDef{
			Name: "log", Category: "math", Pure: true,
			Description: "Return the logarithm of a number in a given base.",
			Params: []ParamDef{
				{Name: "n", Type: "number"},
				{Name: "base", Type: "number"},
			},
			ReturnType: "number",
			Func:       stdlib.LogFunc,
		},
		FunctionDef{
			Name: "pow", Category: "math", Pure: true,
			Description: "Return base raised to the power of exponent.",
			Params: []ParamDef{
				{Name: "base", Type: "number"},
				{Name: "exponent", Type: "number"},
			},
			ReturnType: "number",
			Func:       stdlib.PowFunc,
		},
		FunctionDef{
			Name: "signum", Category: "math", Pure: true,
			Description: "Return -1, 0, or 1 depending on the sign of a number.",
			Params:      []ParamDef{{Name: "n", Type: "number"}},
			ReturnType:  "number",
			Func:        stdlib.SignumFunc,
		},
	)
}
