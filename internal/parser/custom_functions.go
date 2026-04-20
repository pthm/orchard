package parser

import (
	"crypto/rand"
	"fmt"
	"math/big"
	mrand "math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// customFunctions returns Orchard-specific HCL functions that don't
// come from cty's stdlib. Merged into StdFunctions so they're
// available everywhere expressions are evaluated.
func customFunctions() map[string]function.Function {
	return map[string]function.Function{
		"uuid":          uuidFunc(),
		"env":           envFunc(),
		"now":           nowFunc(),
		"time_sub":      timeSubFunc(),
		"time_sequence": timeSequenceFunc(),
	}
}

// uuidFunc returns a random v4 UUID string on every call. Not
// deterministic — each invocation produces a new value. That's
// intentional: scenarios are single-shot imperative runs, not
// convergent plans, so impurity is fine.
func uuidFunc() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			var buf [16]byte
			if _, err := rand.Read(buf[:]); err != nil {
				return cty.NilVal, fmt.Errorf("uuid: %w", err)
			}
			buf[6] = (buf[6] & 0x0f) | 0x40 // version 4
			buf[8] = (buf[8] & 0x3f) | 0x80 // variant 10
			s := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
				buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
			return cty.StringVal(s), nil
		},
	})
}

// envFunc reads an environment variable by name. Returns an empty
// string when the variable is unset — matching shell convention
// (and os.Getenv's semantics) rather than erroring.
//
// Security note: values read via env() can end up in run records
// and plan output. Scenario authors should use env() for
// connection strings and non-secret config, not for raw credentials
// they want to keep out of logs.
func envFunc() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "name", Type: cty.String},
		},
		Type: function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			return cty.StringVal(os.Getenv(args[0].AsString())), nil
		},
	})
}

// nowFunc returns the current UTC time as an RFC3339 string.
// Called once per expression evaluation, so two `now()` calls
// in the same action body may differ by microseconds.
func nowFunc() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			return cty.StringVal(time.Now().UTC().Format(time.RFC3339)), nil
		},
	})
}

// timeSubFunc subtracts a duration from a timestamp. The duration
// string supports Go's standard format (h, m, s, ms) plus
// extended units: d (days = 24h) and w (weeks = 7d).
//
// Equivalent to timeadd(t, "-" + dur) but avoids the double-negative
// awkwardness of "subtract 180 days" → timeadd(t, "-4320h").
func timeSubFunc() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "timestamp", Type: cty.String},
			{Name: "duration", Type: cty.String},
		},
		Type: function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			t, err := time.Parse(time.RFC3339, args[0].AsString())
			if err != nil {
				return cty.NilVal, fmt.Errorf("time_sub: parse timestamp: %w", err)
			}
			d, err := parseExtendedDuration(args[1].AsString())
			if err != nil {
				return cty.NilVal, fmt.Errorf("time_sub: %w", err)
			}
			return cty.StringVal(t.Add(-d).UTC().Format(time.RFC3339)), nil
		},
	})
}

// timeSequenceFunc generates a list of timestamps distributed
// between start and end. Each step advances by a random interval
// between min_interval and max_interval (inclusive). When
// max_interval is omitted, steps are exactly min_interval apart.
//
// The result is a list(string) of RFC3339 timestamps, sorted
// chronologically. The list ends when the next step would exceed
// end.
//
//	time_sequence(
//	  time_sub(now(), "180d"),  # start
//	  now(),                    # end
//	  "1d",                     # min interval
//	  "5d",                     # max interval (optional)
//	)
//
// Returns an empty list when start >= end or the interval is wider
// than the range.
func timeSequenceFunc() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "start", Type: cty.String},
			{Name: "end", Type: cty.String},
			{Name: "min_interval", Type: cty.String},
		},
		VarParam: &function.Parameter{
			Name: "max_interval",
			Type: cty.String,
		},
		Type: function.StaticReturnType(cty.List(cty.String)),
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			start, err := time.Parse(time.RFC3339, args[0].AsString())
			if err != nil {
				return cty.NilVal, fmt.Errorf("time_sequence: parse start: %w", err)
			}
			end, err := time.Parse(time.RFC3339, args[1].AsString())
			if err != nil {
				return cty.NilVal, fmt.Errorf("time_sequence: parse end: %w", err)
			}
			minInterval, err := parseExtendedDuration(args[2].AsString())
			if err != nil {
				return cty.NilVal, fmt.Errorf("time_sequence: min_interval: %w", err)
			}
			maxInterval := minInterval
			if len(args) > 3 {
				maxInterval, err = parseExtendedDuration(args[3].AsString())
				if err != nil {
					return cty.NilVal, fmt.Errorf("time_sequence: max_interval: %w", err)
				}
			}
			if maxInterval < minInterval {
				return cty.NilVal, fmt.Errorf("time_sequence: max_interval (%s) < min_interval (%s)", maxInterval, minInterval)
			}

			// Safety cap: don't generate unbounded lists from
			// tiny intervals across huge ranges.
			const maxEntries = 100_000

			var out []cty.Value
			cursor := start
			for cursor.Before(end) && len(out) < maxEntries {
				out = append(out, cty.StringVal(cursor.UTC().Format(time.RFC3339)))
				step := minInterval
				if maxInterval > minInterval {
					delta := int64(maxInterval - minInterval)
					jitter, _ := cryptoRandInt64(delta)
					step = minInterval + time.Duration(jitter)
				}
				cursor = cursor.Add(step)
			}

			if len(out) == 0 {
				return cty.ListValEmpty(cty.String), nil
			}
			return cty.ListVal(out), nil
		},
	})
}

// parseExtendedDuration parses a Go-style duration string with two
// extensions: "d" for days (24h) and "w" for weeks (7d). Combined
// forms like "1d12h" are not supported — use "36h" instead.
func parseExtendedDuration(s string) (time.Duration, error) {
	// Try Go standard first (handles h, m, s, ms, us, ns).
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}

	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	if strings.HasSuffix(s, "w") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "w"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(n * float64(7*24*time.Hour)), nil
	}
	return 0, fmt.Errorf("invalid duration %q (expected Go duration, or Nd/Nw for days/weeks)", s)
}

// cryptoRandInt64 returns a cryptographically random int64 in [0, max).
// Falls back to math/rand if crypto/rand fails — fine for interval
// jitter, not for security.
func cryptoRandInt64(max int64) (int64, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(max))
	if err != nil {
		return mrand.Int63n(max), nil //nolint:gosec
	}
	return n.Int64(), nil
}
