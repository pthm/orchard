package language

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"
)

// Helper: call a function by name from the registry.
func callFunc(t *testing.T, name string, args ...cty.Value) cty.Value {
	t.Helper()
	fn, ok := ByName(name)
	if !ok {
		t.Fatalf("function %q not registered", name)
	}
	result, err := fn.Func.Call(args)
	if err != nil {
		t.Fatalf("%s(%v): %v", name, args, err)
	}
	return result
}

func callFuncErr(t *testing.T, name string, args ...cty.Value) error {
	t.Helper()
	fn, ok := ByName(name)
	if !ok {
		t.Fatalf("function %q not registered", name)
	}
	_, err := fn.Func.Call(args)
	return err
}

// ── uuid ────────────────────────────────────────────────────────

func TestUUID_Format(t *testing.T) {
	v := callFunc(t, "uuid")
	s := v.AsString()
	if len(s) != 36 {
		t.Fatalf("uuid length: got %d want 36", len(s))
	}
	// Version 4: char at index 14 must be '4'
	if s[14] != '4' {
		t.Errorf("uuid version nibble: got %c want 4", s[14])
	}
	// Variant: char at index 19 must be 8, 9, a, or b
	if !strings.ContainsRune("89ab", rune(s[19])) {
		t.Errorf("uuid variant nibble: got %c want [89ab]", s[19])
	}
}

func TestUUID_Unique(t *testing.T) {
	a := callFunc(t, "uuid").AsString()
	b := callFunc(t, "uuid").AsString()
	if a == b {
		t.Errorf("two uuid() calls produced the same value: %s", a)
	}
}

// ── env ─────────────────────────────────────────────────────────

func TestEnv_Set(t *testing.T) {
	t.Setenv("ORCHARD_TEST_X", "hello")
	v := callFunc(t, "env", cty.StringVal("ORCHARD_TEST_X"))
	if v.AsString() != "hello" {
		t.Errorf("env(set): got %q", v.AsString())
	}
}

func TestEnv_Unset(t *testing.T) {
	os.Unsetenv("ORCHARD_TEST_MISSING_12345")
	v := callFunc(t, "env", cty.StringVal("ORCHARD_TEST_MISSING_12345"))
	if v.AsString() != "" {
		t.Errorf("env(unset): got %q want empty", v.AsString())
	}
}

// ── now ─────────────────────────────────────────────────────────

func TestNow_RFC3339(t *testing.T) {
	before := time.Now().UTC()
	v := callFunc(t, "now")
	after := time.Now().UTC()

	ts, err := time.Parse(time.RFC3339, v.AsString())
	if err != nil {
		t.Fatalf("now() not RFC3339: %v", err)
	}
	if ts.Before(before.Add(-time.Second)) || ts.After(after.Add(time.Second)) {
		t.Errorf("now() = %s, outside [%s, %s]", ts, before, after)
	}
}

// ── timesub ─────────────────────────────────────────────────────

func TestTimeSub_Days(t *testing.T) {
	v := callFunc(t, "timesub",
		cty.StringVal("2026-04-20T12:00:00Z"),
		cty.StringVal("3d"))
	if v.AsString() != "2026-04-17T12:00:00Z" {
		t.Errorf("timesub 3d: got %q", v.AsString())
	}
}

func TestTimeSub_Weeks(t *testing.T) {
	v := callFunc(t, "timesub",
		cty.StringVal("2026-04-20T12:00:00Z"),
		cty.StringVal("2w"))
	if v.AsString() != "2026-04-06T12:00:00Z" {
		t.Errorf("timesub 2w: got %q", v.AsString())
	}
}

func TestTimeSub_Hours(t *testing.T) {
	v := callFunc(t, "timesub",
		cty.StringVal("2026-04-20T12:00:00Z"),
		cty.StringVal("6h"))
	if v.AsString() != "2026-04-20T06:00:00Z" {
		t.Errorf("timesub 6h: got %q", v.AsString())
	}
}

func TestTimeSub_FractionalDays(t *testing.T) {
	v := callFunc(t, "timesub",
		cty.StringVal("2026-04-20T12:00:00Z"),
		cty.StringVal("0.5d"))
	if v.AsString() != "2026-04-20T00:00:00Z" {
		t.Errorf("timesub 0.5d: got %q", v.AsString())
	}
}

func TestTimeSub_InvalidTimestamp(t *testing.T) {
	err := callFuncErr(t, "timesub",
		cty.StringVal("not-a-timestamp"),
		cty.StringVal("1d"))
	if err == nil {
		t.Fatal("expected error for invalid timestamp")
	}
}

func TestTimeSub_InvalidDuration(t *testing.T) {
	err := callFuncErr(t, "timesub",
		cty.StringVal("2026-04-20T12:00:00Z"),
		cty.StringVal("xyz"))
	if err == nil {
		t.Fatal("expected error for invalid duration")
	}
}

// ── timeunix ────────────────────────────────────────────────────

func TestTimeUnix_Epoch(t *testing.T) {
	v := callFunc(t, "timeunix", cty.StringVal("1970-01-01T00:00:00Z"))
	n, _ := v.AsBigFloat().Int64()
	if n != 0 {
		t.Errorf("timeunix epoch: got %d want 0", n)
	}
}

func TestTimeUnix_KnownDate(t *testing.T) {
	v := callFunc(t, "timeunix", cty.StringVal("2026-01-01T00:00:00Z"))
	n, _ := v.AsBigFloat().Int64()
	want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	if n != want {
		t.Errorf("timeunix 2026: got %d want %d", n, want)
	}
}

func TestTimeUnix_InvalidTimestamp(t *testing.T) {
	err := callFuncErr(t, "timeunix", cty.StringVal("nope"))
	if err == nil {
		t.Fatal("expected error for invalid timestamp")
	}
}

// ── timeseries ──────────────────────────────────────────────────

func TestTimeSeries_ExactCount(t *testing.T) {
	v := callFunc(t, "timeseries",
		cty.StringVal("2026-01-01T00:00:00Z"),
		cty.StringVal("2026-01-10T00:00:00Z"),
		cty.NumberIntVal(10))
	if v.LengthInt() != 10 {
		t.Errorf("timeseries count=10: got %d", v.LengthInt())
	}
}

func TestTimeSeries_FirstAndLast(t *testing.T) {
	v := callFunc(t, "timeseries",
		cty.StringVal("2026-01-01T00:00:00Z"),
		cty.StringVal("2026-01-10T00:00:00Z"),
		cty.NumberIntVal(5))

	it := v.ElementIterator()
	it.Next()
	_, first := it.Element()
	if first.AsString() != "2026-01-01T00:00:00Z" {
		t.Errorf("first: got %q", first.AsString())
	}
	var last string
	for it.Next() {
		_, el := it.Element()
		last = el.AsString()
	}
	if last != "2026-01-10T00:00:00Z" {
		t.Errorf("last: got %q", last)
	}
}

func TestTimeSeries_CountZero(t *testing.T) {
	v := callFunc(t, "timeseries",
		cty.StringVal("2026-01-01T00:00:00Z"),
		cty.StringVal("2026-01-10T00:00:00Z"),
		cty.NumberIntVal(0))
	if v.LengthInt() != 0 {
		t.Errorf("timeseries count=0: got %d", v.LengthInt())
	}
}

func TestTimeSeries_CountOne(t *testing.T) {
	v := callFunc(t, "timeseries",
		cty.StringVal("2026-01-01T00:00:00Z"),
		cty.StringVal("2026-12-31T00:00:00Z"),
		cty.NumberIntVal(1))
	if v.LengthInt() != 1 {
		t.Errorf("timeseries count=1: got %d", v.LengthInt())
	}
}

func TestTimeSeries_StartAfterEnd(t *testing.T) {
	v := callFunc(t, "timeseries",
		cty.StringVal("2026-12-31T00:00:00Z"),
		cty.StringVal("2026-01-01T00:00:00Z"),
		cty.NumberIntVal(5))
	if v.LengthInt() != 0 {
		t.Errorf("timeseries start>end: got %d entries, want 0", v.LengthInt())
	}
}

// ── timesequence ────────────────────────────────────────────────

func TestTimeSequence_EvenInterval(t *testing.T) {
	v := callFunc(t, "timesequence",
		cty.StringVal("2026-01-01T00:00:00Z"),
		cty.StringVal("2026-01-11T00:00:00Z"),
		cty.StringVal("2d"))
	// 10 days / 2d interval = 5 entries (days 0, 2, 4, 6, 8)
	if v.LengthInt() != 5 {
		t.Errorf("timesequence 2d: got %d want 5", v.LengthInt())
	}
}

func TestTimeSequence_RandomInterval(t *testing.T) {
	v := callFunc(t, "timesequence",
		cty.StringVal("2026-01-01T00:00:00Z"),
		cty.StringVal("2026-01-11T00:00:00Z"),
		cty.StringVal("1d"),
		cty.StringVal("3d"))
	if v.LengthInt() < 1 {
		t.Error("timesequence random: expected at least 1 entry")
	}
	// Verify chronological ordering
	it := v.ElementIterator()
	var prev time.Time
	for it.Next() {
		_, el := it.Element()
		ts, _ := time.Parse(time.RFC3339, el.AsString())
		if !prev.IsZero() && ts.Before(prev) {
			t.Errorf("out of order: %s after %s", ts, prev)
		}
		prev = ts
	}
}

func TestTimeSequence_StartAfterEnd(t *testing.T) {
	v := callFunc(t, "timesequence",
		cty.StringVal("2026-12-31T00:00:00Z"),
		cty.StringVal("2026-01-01T00:00:00Z"),
		cty.StringVal("1d"))
	if v.LengthInt() != 0 {
		t.Errorf("timesequence start>end: got %d entries", v.LengthInt())
	}
}

func TestTimeSequence_MaxLessThanMin(t *testing.T) {
	err := callFuncErr(t, "timesequence",
		cty.StringVal("2026-01-01T00:00:00Z"),
		cty.StringVal("2026-01-11T00:00:00Z"),
		cty.StringVal("5d"),
		cty.StringVal("1d"))
	if err == nil {
		t.Fatal("expected error when max < min")
	}
}

// ── timerandom ──────────────────────────────────────────────────

func TestTimeRandom_WithinBounds(t *testing.T) {
	start := "2026-01-01T00:00:00Z"
	end := "2026-12-31T23:59:59Z"
	startT, _ := time.Parse(time.RFC3339, start)
	endT, _ := time.Parse(time.RFC3339, end)

	for i := 0; i < 10; i++ {
		v := callFunc(t, "timerandom", cty.StringVal(start), cty.StringVal(end))
		ts, err := time.Parse(time.RFC3339, v.AsString())
		if err != nil {
			t.Fatalf("timerandom parse: %v", err)
		}
		if ts.Before(startT) || ts.After(endT) {
			t.Errorf("timerandom: %s outside [%s, %s]", ts, start, end)
		}
	}
}

func TestTimeRandom_SameStartEnd(t *testing.T) {
	ts := "2026-06-15T00:00:00Z"
	v := callFunc(t, "timerandom", cty.StringVal(ts), cty.StringVal(ts))
	if v.AsString() != ts {
		t.Errorf("timerandom same: got %q want %q", v.AsString(), ts)
	}
}

func TestTimeRandom_StartAfterEnd(t *testing.T) {
	v := callFunc(t, "timerandom",
		cty.StringVal("2026-12-31T00:00:00Z"),
		cty.StringVal("2026-01-01T00:00:00Z"))
	// Should return start when start >= end
	if v.AsString() != "2026-12-31T00:00:00Z" {
		t.Errorf("timerandom start>end: got %q", v.AsString())
	}
}

// ── registry completeness ───────────────────────────────────────

func TestAllCustomFunctionsRegistered(t *testing.T) {
	expected := []string{
		"uuid", "env", "now",
		"timesub", "timesequence", "timeseries", "timeunix", "timerandom",
	}
	for _, name := range expected {
		if _, ok := ByName(name); !ok {
			t.Errorf("custom function %q not found in registry", name)
		}
	}
}

func TestCustomFunctionMetadata(t *testing.T) {
	// Every custom function should have description, return type, and category.
	customs := []string{
		"uuid", "env", "now",
		"timesub", "timesequence", "timeseries", "timeunix", "timerandom",
	}
	for _, name := range customs {
		fn, ok := ByName(name)
		if !ok {
			t.Errorf("%q: not registered", name)
			continue
		}
		if fn.Description == "" {
			t.Errorf("%q: missing description", name)
		}
		if fn.ReturnType == "" {
			t.Errorf("%q: missing return type", name)
		}
		if fn.Category == "" {
			t.Errorf("%q: missing category", name)
		}
	}
}
