package parser

import (
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

// Functions returns the complete set of HCL functions Orchard
// exposes to expressions — variable defaults, provider configs,
// component inputs, action attributes, scenario outputs, component
// outputs, and teardown bodies. Every EvalContext in the parser,
// engine, and plan-time validators pulls from this single source.
//
// All names use lowercase compound words with no separators,
// matching the convention established by HCL's cty function
// library: jsonencode, formatdate, timeadd, etc. Orchard-specific
// functions (uuid, env, timesub, timesequence, ...) follow the
// same pattern.
//
// Expanding the set is safe (additive for existing scenarios).
// Shrinking or renaming is a breaking change.
//
// A fresh map is returned each call so callers can overlay
// scenario-specific helpers without polluting the shared set.
func Functions() map[string]function.Function {
	m := map[string]function.Function{
		// ── Strings ──────────────────────────────────────────────
		"upper":       stdlib.UpperFunc,
		"lower":       stdlib.LowerFunc,
		"title":       stdlib.TitleFunc,
		"trim":        stdlib.TrimFunc,
		"trimspace":  stdlib.TrimSpaceFunc,
		"trimprefix": stdlib.TrimPrefixFunc,
		"trimsuffix": stdlib.TrimSuffixFunc,
		"substr":      stdlib.SubstrFunc,
		"replace":     stdlib.ReplaceFunc,
		"format":      stdlib.FormatFunc,
		"formatlist": stdlib.FormatListFunc,
		"split":       stdlib.SplitFunc,
		"join":        stdlib.JoinFunc,
		"chomp":       stdlib.ChompFunc,
		"strlen":      stdlib.StrlenFunc,

		// ── Collections ─────────────────────────────────────────
		"concat":   stdlib.ConcatFunc,
		"length":   stdlib.LengthFunc,
		"keys":     stdlib.KeysFunc,
		"values":   stdlib.ValuesFunc,
		"lookup":   stdlib.LookupFunc,
		"contains": stdlib.ContainsFunc,
		"merge":    stdlib.MergeFunc,
		"reverse":  stdlib.ReverseListFunc,
		"sort":     stdlib.SortFunc,
		"slice":    stdlib.SliceFunc,
		"distinct": stdlib.DistinctFunc,
		"element":  stdlib.ElementFunc,
		"range":    stdlib.RangeFunc,
		"zipmap":   stdlib.ZipmapFunc,

		// ── Encoding ────────────────────────────────────────────
		"jsonencode": stdlib.JSONEncodeFunc,
		"jsondecode": stdlib.JSONDecodeFunc,
		"csvdecode":  stdlib.CSVDecodeFunc,

		// ── Math ────────────────────────────────────────────────
		"abs":    stdlib.AbsoluteFunc,
		"ceil":   stdlib.CeilFunc,
		"floor":  stdlib.FloorFunc,
		"max":    stdlib.MaxFunc,
		"min":    stdlib.MinFunc,
		"log":    stdlib.LogFunc,
		"pow":    stdlib.PowFunc,
		"signum": stdlib.SignumFunc,

		// ── Regex ───────────────────────────────────────────────
		"regex":     stdlib.RegexFunc,
		"regexall": stdlib.RegexAllFunc,

		// ── Time ────────────────────────────────────────────────
		"formatdate": stdlib.FormatDateFunc,
		"timeadd":    stdlib.TimeAddFunc,

		// ── Sets ────────────────────────────────────────────────
		"setintersection": stdlib.SetIntersectionFunc,
		"setproduct":      stdlib.SetProductFunc,
		"setsubtract":     stdlib.SetSubtractFunc,
		"setunion":        stdlib.SetUnionFunc,
	}

	// Orchard-specific functions: uuid, env, time utilities.
	for name, fn := range customFunctions() {
		m[name] = fn
	}
	return m
}

