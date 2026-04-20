package language

func init() {
	register(
		FunctionDef{
			Name: "env", Category: "environment", Pure: false,
			Description: "Read an environment variable by name.",
			Detail:      "Returns an empty string when the variable is unset, matching shell convention. Values read via `env()` may appear in run records — use for connection strings and config, not raw secrets.",
			Params:      []ParamDef{{Name: "name", Type: "string", Description: "Environment variable name."}},
			ReturnType:  "string",
			Examples: []ExampleDef{
				{Code: `env("DATABASE_URL")`, Result: `"postgres://localhost/mydb"`},
				{Code: `env("UNSET_VAR")`, Result: `""`},
			},
			Func: CustomFunc("env"),
		},
	)
}
