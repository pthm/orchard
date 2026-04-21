package language

import (
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

func init() {
	// customFuncs is used to pull our implementations by name.
	// CustomFunctionByName is the lookup; see below.

	register(
		FunctionDef{
			Name: "now", Category: "time", Pure: false,
			Description: "Return the current UTC time as an RFC3339 string.",
			Detail:      "Evaluated at expression-evaluation time. Two `now()` calls in the same action body may differ by microseconds.",
			ReturnType:  "string",
			Examples:    []ExampleDef{{Code: `now()`, Result: `"2026-04-20T12:00:00Z"`}},
			Func:        CustomFunc("now"),
		},
		FunctionDef{
			Name: "timeadd", Category: "time", Pure: true,
			Description: "Add a Go duration to a timestamp.",
			Params: []ParamDef{
				{Name: "timestamp", Type: "string", Description: "RFC3339 timestamp."},
				{Name: "duration", Type: "string", Description: "Go duration (e.g. \"6h\", \"30m\")."},
			},
			ReturnType: "string",
			Examples:   []ExampleDef{{Code: `timeadd("2026-01-01T00:00:00Z", "24h")`, Result: `"2026-01-02T00:00:00Z"`}},
			Func:       stdlib.TimeAddFunc,
		},
		FunctionDef{
			Name: "timesub", Category: "time", Pure: true,
			Description: "Subtract a duration from a timestamp.",
			Detail:      "Supports extended durations: `d` (days = 24h) and `w` (weeks = 7d) on top of Go's standard units.",
			Params: []ParamDef{
				{Name: "timestamp", Type: "string", Description: "RFC3339 timestamp."},
				{Name: "duration", Type: "string", Description: "Duration (e.g. \"30d\", \"2w\", \"6h\")."},
			},
			ReturnType: "string",
			Examples:   []ExampleDef{{Code: `timesub("2026-04-20T00:00:00Z", "7d")`, Result: `"2026-04-13T00:00:00Z"`}},
			Func:       CustomFunc("timesub"),
		},
		FunctionDef{
			Name: "formatdate", Category: "time", Pure: true,
			Description: "Format a timestamp using a format string.",
			Detail:      "Uses HCL's format spec: `YYYY` (year), `MM` (month), `DD` (day), `hh` (hour), `mm` (minute), `ss` (second).",
			Params: []ParamDef{
				{Name: "format", Type: "string"},
				{Name: "timestamp", Type: "string", Description: "RFC3339 timestamp."},
			},
			ReturnType: "string",
			Examples:   []ExampleDef{{Code: `formatdate("YYYY-MM-DD", "2026-04-20T12:30:00Z")`, Result: `"2026-04-20"`}},
			Func:       stdlib.FormatDateFunc,
		},
		FunctionDef{
			Name: "timeunix", Category: "time", Pure: true,
			Description: "Convert a timestamp to Unix epoch seconds.",
			Params: []ParamDef{
				{Name: "timestamp", Type: "string", Description: "RFC3339 timestamp."},
			},
			ReturnType: "number",
			Examples:   []ExampleDef{{Code: `timeunix("2026-01-01T00:00:00Z")`, Result: `1767225600`}},
			Func:       CustomFunc("timeunix"),
		},
		FunctionDef{
			Name: "timerandom", Category: "time", Pure: false,
			Description: "Return a random timestamp between start and end.",
			Params: []ParamDef{
				{Name: "start", Type: "string", Description: "RFC3339 timestamp (inclusive)."},
				{Name: "end", Type: "string", Description: "RFC3339 timestamp (exclusive)."},
			},
			ReturnType: "string",
			Examples:   []ExampleDef{{Code: `timerandom(timesub(now(), "30d"), now())`}},
			Func:       CustomFunc("timerandom"),
		},
		FunctionDef{
			Name: "timeseries", Category: "time", Pure: true,
			Description: "Distribute exactly N timestamps evenly between start and end.",
			Detail:      "First entry is start, last is end, intermediate points are equidistant. Use `timesequence` for interval-based generation.",
			Params: []ParamDef{
				{Name: "start", Type: "string", Description: "RFC3339 timestamp."},
				{Name: "end", Type: "string", Description: "RFC3339 timestamp."},
				{Name: "count", Type: "number", Description: "Number of timestamps to generate."},
			},
			ReturnType: "list(string)",
			Examples:   []ExampleDef{{Code: `timeseries(timesub(now(), "90d"), now(), 10)`, Result: `["2026-01-20T...", ..., "2026-04-20T..."]`}},
			Func:       CustomFunc("timeseries"),
		},
		FunctionDef{
			Name: "timesequence", Category: "time", Pure: false,
			Description: "Generate timestamps stepping by random intervals between min and max.",
			Detail:      "Produces timestamps from start until end is reached. With only min_interval, steps are evenly spaced. With both min and max, each step is randomised within the range. Capped at 100,000 entries.",
			Params: []ParamDef{
				{Name: "start", Type: "string", Description: "RFC3339 timestamp."},
				{Name: "end", Type: "string", Description: "RFC3339 timestamp."},
				{Name: "min_interval", Type: "string", Description: "Minimum step duration."},
			},
			VarParam:   &ParamDef{Name: "max_interval", Type: "string", Description: "Maximum step duration (optional)."},
			ReturnType: "list(string)",
			Examples:   []ExampleDef{{Code: `timesequence(timesub(now(), "30d"), now(), "1d", "5d")`}},
			Func:       CustomFunc("timesequence"),
		},
	)
}
