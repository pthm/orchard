package language

func init() {
	register(
		FunctionDef{
			Name: "uuid", Category: "identity", Pure: false,
			Description: "Generate a random v4 UUID string.",
			Detail:      "Each call produces a new value. Use for primary keys, correlation IDs, or any field that needs a unique identifier.",
			ReturnType:  "string",
			Examples:    []ExampleDef{{Code: `uuid()`, Result: `"a3f29e5b-1234-4abc-9def-0123456789ab"`}},
			Random:      uuidFunc,
		},
	)
}
