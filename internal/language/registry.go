// Package language defines Orchard's HCL function registry — the
// single source of truth for every function available in scenario
// expressions. Each entry carries the implementation (a cty
// function.Function) alongside structured metadata: description,
// parameter signatures, return type, examples, and purity.
//
// Multiple consumers read this registry:
//
//   - parser.Functions() builds the map[string]function.Function the
//     engine evaluates expressions against.
//   - The docs generator (orchard docs functions, just docs-generate)
//     renders the function reference page from the metadata.
//   - A future LSP server will serve completions and hover docs
//     directly from the same metadata.
//   - orchard functions --json exports the registry as a JSON
//     manifest for editor grammars and external tooling.
//
// To add a function: append a FunctionDef to the registry slice in
// registry_*.go. The implementation and documentation live together
// so neither drifts.
package language

import (
	"sort"

	"github.com/zclconf/go-cty/cty/function"
)

// FunctionDef is one function in the registry. It bundles the cty
// implementation with the metadata tooling needs.
type FunctionDef struct {
	// Name is the function name as scenario authors write it:
	// "jsonencode", "timesub", "uuid", etc. Lowercase compound
	// words, no separators.
	Name string `json:"name"`

	// Category groups functions in docs and completion menus.
	// Conventional values: "strings", "collections", "encoding",
	// "math", "regex", "time", "identity", "environment", "sets".
	Category string `json:"category"`

	// Description is a one-liner shown in completion menus and
	// at the top of a docs entry.
	Description string `json:"description"`

	// Detail is the full documentation for hover popups and the
	// reference page. Markdown.
	Detail string `json:"detail,omitempty"`

	// Params lists the positional parameters in order.
	Params []ParamDef `json:"params"`

	// VarParam describes the variadic trailing parameter, if any.
	// nil when the function takes a fixed number of args.
	VarParam *ParamDef `json:"var_param,omitempty"`

	// ReturnType is a human-readable type string for display:
	// "string", "number", "list(string)", etc.
	ReturnType string `json:"return_type"`

	// Pure is true when the function is deterministic — same
	// inputs always produce the same output. False for uuid, env,
	// now, timerandom, timesequence (randomised intervals).
	Pure bool `json:"pure"`

	// Examples are short usage snippets. Each has a Code string
	// (the expression) and an optional Result (what it produces).
	Examples []ExampleDef `json:"examples,omitempty"`

	// Func is the cty function implementation. Omitted from JSON
	// serialisation — tooling that imports this package calls it
	// directly; external consumers (grammars, docs) only need the
	// metadata.
	Func function.Function `json:"-"`
}

// ParamDef describes one parameter.
type ParamDef struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// ExampleDef is a worked usage snippet.
type ExampleDef struct {
	Code   string `json:"code"`
	Result string `json:"result,omitempty"`
}

// registry is the authoritative, ordered list of all functions.
// Populated by init() in the registry_*.go files.
var registry []FunctionDef

// register appends defs to the global registry. Called from init()
// in each registry_*.go file so functions are grouped by category.
func register(defs ...FunctionDef) {
	registry = append(registry, defs...)
}

// All returns every registered function, sorted by name.
func All() []FunctionDef {
	out := make([]FunctionDef, len(registry))
	copy(out, registry)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// ByName returns the function with the given name, or false.
func ByName(name string) (FunctionDef, bool) {
	for _, d := range registry {
		if d.Name == name {
			return d, true
		}
	}
	return FunctionDef{}, false
}

// Map builds the function.Function map the parser/engine use as
// an EvalContext.Functions value. This is the bridge between the
// metadata-rich registry and the engine's bare function dispatch.
func Map() map[string]function.Function {
	m := make(map[string]function.Function, len(registry))
	for _, d := range registry {
		m[d.Name] = d.Func
	}
	return m
}

// Categories returns the distinct category names in sorted order.
func Categories() []string {
	seen := map[string]bool{}
	for _, d := range registry {
		seen[d.Category] = true
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// ByCategory returns all functions in the given category, sorted
// by name.
func ByCategory(category string) []FunctionDef {
	var out []FunctionDef
	for _, d := range registry {
		if d.Category == category {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}
