package parser

import (
	"github.com/zclconf/go-cty/cty/function"

	"github.com/pthm/orchard/internal/language"
)

// Functions returns the complete set of HCL functions Orchard
// exposes to expressions. Delegates to language.Map() which builds
// the function map from the unified registry — the single source
// of truth for function implementations and metadata.
//
// All callers (parser, engine, plan-time validators, teardown)
// use this function to populate EvalContext.Functions.
func Functions() map[string]function.Function {
	return language.Map()
}
