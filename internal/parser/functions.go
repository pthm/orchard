package parser

import (
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

// StdFunctions returns the HCL functions Orchard makes available to
// every expression in a scenario or component. The returned map is
// the single source of truth: every EvalContext constructed by the
// parser, engine, or plan-time validators pulls from here, so
// adding an entry here makes the function available everywhere.
//
// Selection rationale: the set mirrors a curated subset of
// Terraform's function library — common string/collection/encoding
// helpers plus some math and regex. Nothing side-effectful (no
// file/env/http access); scenarios stay pure transformations over
// declared inputs.
//
// Expanding the set is safe for existing scenarios (functions names
// can't collide with HCL keywords or references). Shrinking it is
// a breaking change.
//
// A fresh map is returned each call so callers are free to merge
// scenario-specific helpers on top without polluting the shared set.
func StdFunctions() map[string]function.Function {
	return map[string]function.Function{
		// Strings
		"upper":      stdlib.UpperFunc,
		"lower":      stdlib.LowerFunc,
		"title":      stdlib.TitleFunc,
		"trim":       stdlib.TrimFunc,
		"trimspace":  stdlib.TrimSpaceFunc,
		"trimprefix": stdlib.TrimPrefixFunc,
		"trimsuffix": stdlib.TrimSuffixFunc,
		"substr":     stdlib.SubstrFunc,
		"replace":    stdlib.ReplaceFunc,
		"format":     stdlib.FormatFunc,
		"formatlist": stdlib.FormatListFunc,
		"split":      stdlib.SplitFunc,
		"join":       stdlib.JoinFunc,
		"chomp":      stdlib.ChompFunc,
		"strlen":     stdlib.StrlenFunc,

		// Collections
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

		// Encoding
		"jsonencode": stdlib.JSONEncodeFunc,
		"jsondecode": stdlib.JSONDecodeFunc,
		"csvdecode":  stdlib.CSVDecodeFunc,

		// Math
		"abs":    stdlib.AbsoluteFunc,
		"ceil":   stdlib.CeilFunc,
		"floor":  stdlib.FloorFunc,
		"max":    stdlib.MaxFunc,
		"min":    stdlib.MinFunc,
		"log":    stdlib.LogFunc,
		"pow":    stdlib.PowFunc,
		"signum": stdlib.SignumFunc,

		// Regex
		"regex":    stdlib.RegexFunc,
		"regexall": stdlib.RegexAllFunc,

		// Time
		"formatdate": stdlib.FormatDateFunc,
		"timeadd":    stdlib.TimeAddFunc,

		// Sets
		"setintersection": stdlib.SetIntersectionFunc,
		"setproduct":      stdlib.SetProductFunc,
		"setsubtract":     stdlib.SetSubtractFunc,
		"setunion":        stdlib.SetUnionFunc,
	}
}
