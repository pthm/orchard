package language

import "github.com/zclconf/go-cty/cty/function/stdlib"

func init() {
	register(
		FunctionDef{
			Name: "jsonencode", Category: "encoding", Pure: true,
			Description: "Encode a value as a JSON string.",
			Params:      []ParamDef{{Name: "value", Type: "any"}},
			ReturnType:  "string",
			Examples:    []ExampleDef{{Code: `jsonencode({name = "alice", age = 30})`, Result: `"{\"name\":\"alice\",\"age\":30}"`}},
			Func:        stdlib.JSONEncodeFunc,
		},
		FunctionDef{
			Name: "jsondecode", Category: "encoding", Pure: true,
			Description: "Decode a JSON string into a value.",
			Params:      []ParamDef{{Name: "str", Type: "string"}},
			ReturnType:  "any",
			Func:        stdlib.JSONDecodeFunc,
		},
		FunctionDef{
			Name: "csvdecode", Category: "encoding", Pure: true,
			Description: "Decode a CSV string into a list of maps (first row = headers).",
			Params:      []ParamDef{{Name: "str", Type: "string"}},
			ReturnType:  "list(map(string))",
			Func:        stdlib.CSVDecodeFunc,
		},
	)
}
