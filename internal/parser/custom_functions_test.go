package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUUIDFunction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "s" {
  variable "id1" { default = uuid() }
  variable "id2" { default = uuid() }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ps, err := LoadScenario(path, "", nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}

	id1 := ps.Variables["id1"].AsString()
	id2 := ps.Variables["id2"].AsString()

	// UUID v4 format: 8-4-4-4-12 hex chars
	if len(id1) != 36 || id1[8] != '-' || id1[13] != '-' {
		t.Errorf("uuid() produced invalid format: %q", id1)
	}
	if id1 == id2 {
		t.Errorf("two uuid() calls should produce different values, got %q both times", id1)
	}
}

func TestEnvFunction(t *testing.T) {
	t.Setenv("ORCHARD_TEST_VAR", "hello-from-env")

	dir := t.TempDir()
	path := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "s" {
  variable "from_env" { default = env("ORCHARD_TEST_VAR") }
  variable "missing"  { default = env("ORCHARD_TEST_MISSING") }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ps, err := LoadScenario(path, "", nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}

	if got := ps.Variables["from_env"].AsString(); got != "hello-from-env" {
		t.Errorf("env(set): got %q want %q", got, "hello-from-env")
	}
	if got := ps.Variables["missing"].AsString(); got != "" {
		t.Errorf("env(unset): got %q want empty", got)
	}
}

func TestNowFunction(t *testing.T) {
	before := time.Now().UTC()

	dir := t.TempDir()
	path := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "s" {
  variable "ts" { default = now() }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ps, err := LoadScenario(path, "", nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}

	ts, err := time.Parse(time.RFC3339, ps.Variables["ts"].AsString())
	if err != nil {
		t.Fatalf("parse now(): %v", err)
	}
	after := time.Now().UTC()

	if ts.Before(before.Add(-time.Second)) || ts.After(after.Add(time.Second)) {
		t.Errorf("now() = %s, expected between %s and %s", ts, before, after)
	}
}

func TestTimeSubFunction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "s" {
  variable "base"     { default = "2026-04-20T12:00:00Z" }
  variable "minus_1d" { default = time_sub("2026-04-20T12:00:00Z", "1d") }
  variable "minus_2w" { default = time_sub("2026-04-20T12:00:00Z", "2w") }
  variable "minus_6h" { default = time_sub("2026-04-20T12:00:00Z", "6h") }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ps, err := LoadScenario(path, "", nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}

	cases := map[string]string{
		"minus_1d": "2026-04-19T12:00:00Z",
		"minus_2w": "2026-04-06T12:00:00Z",
		"minus_6h": "2026-04-20T06:00:00Z",
	}
	for name, want := range cases {
		got := ps.Variables[name].AsString()
		if got != want {
			t.Errorf("%s: got %q want %q", name, got, want)
		}
	}
}

func TestTimeSequenceFunction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "s" {
  # 10 days at 2-day intervals = 5 timestamps (days 0, 2, 4, 6, 8)
  variable "even" {
    default = time_sequence("2026-01-01T00:00:00Z", "2026-01-11T00:00:00Z", "2d")
  }
  # Same range but randomized intervals between 1d and 3d
  variable "random" {
    default = time_sequence("2026-01-01T00:00:00Z", "2026-01-11T00:00:00Z", "1d", "3d")
  }
  # Empty: start >= end
  variable "empty" {
    default = time_sequence("2026-01-11T00:00:00Z", "2026-01-01T00:00:00Z", "1d")
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ps, err := LoadScenario(path, "", nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}

	// Even spacing: 5 entries, 2 days apart.
	even := ps.Variables["even"]
	if even.LengthInt() != 5 {
		t.Errorf("even: expected 5 timestamps, got %d", even.LengthInt())
	}

	// Random spacing: at least 1 entry (the start), but count varies.
	random := ps.Variables["random"]
	if random.LengthInt() < 1 {
		t.Error("random: expected at least 1 timestamp")
	}
	// Verify chronological ordering.
	it := random.ElementIterator()
	var prev time.Time
	for it.Next() {
		_, v := it.Element()
		ts, err := time.Parse(time.RFC3339, v.AsString())
		if err != nil {
			t.Fatalf("random: parse: %v", err)
		}
		if !prev.IsZero() && ts.Before(prev) {
			t.Errorf("random: out of order: %s after %s", ts, prev)
		}
		prev = ts
	}

	// Empty: start >= end.
	empty := ps.Variables["empty"]
	if empty.LengthInt() != 0 {
		t.Errorf("empty: expected 0 timestamps, got %d", empty.LengthInt())
	}
}

func TestTimeSeriesFunction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "s" {
  variable "ten" {
    default = time_series("2026-01-01T00:00:00Z", "2026-01-10T00:00:00Z", 10)
  }
  variable "one" {
    default = time_series("2026-01-01T00:00:00Z", "2026-01-10T00:00:00Z", 1)
  }
  variable "zero" {
    default = time_series("2026-01-01T00:00:00Z", "2026-01-10T00:00:00Z", 0)
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ps, err := LoadScenario(path, "", nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}

	ten := ps.Variables["ten"]
	if ten.LengthInt() != 10 {
		t.Errorf("time_series count=10: got %d entries", ten.LengthInt())
	}
	// First should be start, last should be end.
	it := ten.ElementIterator()
	it.Next()
	_, first := it.Element()
	if first.AsString() != "2026-01-01T00:00:00Z" {
		t.Errorf("first: got %q", first.AsString())
	}
	// Advance to last.
	var last string
	for it.Next() {
		_, v := it.Element()
		last = v.AsString()
	}
	if last != "2026-01-10T00:00:00Z" {
		t.Errorf("last: got %q want 2026-01-10T00:00:00Z", last)
	}

	one := ps.Variables["one"]
	if one.LengthInt() != 1 {
		t.Errorf("time_series count=1: got %d entries", one.LengthInt())
	}

	zero := ps.Variables["zero"]
	if zero.LengthInt() != 0 {
		t.Errorf("time_series count=0: got %d entries", zero.LengthInt())
	}
}

func TestTimeUnixFunction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "s" {
  variable "epoch" { default = time_unix("2026-01-01T00:00:00Z") }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ps, err := LoadScenario(path, "", nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}

	epoch, _ := ps.Variables["epoch"].AsBigFloat().Int64()
	want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	if epoch != want {
		t.Errorf("time_unix: got %d want %d", epoch, want)
	}
}

func TestTimeRandomFunction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "s" {
  variable "r1" { default = time_random("2026-01-01T00:00:00Z", "2026-12-31T23:59:59Z") }
  variable "r2" { default = time_random("2026-01-01T00:00:00Z", "2026-12-31T23:59:59Z") }
  # Same start and end: should return start.
  variable "same" { default = time_random("2026-06-15T00:00:00Z", "2026-06-15T00:00:00Z") }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ps, err := LoadScenario(path, "", nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)

	for _, name := range []string{"r1", "r2"} {
		ts, err := time.Parse(time.RFC3339, ps.Variables[name].AsString())
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		if ts.Before(start) || ts.After(end) {
			t.Errorf("%s: %s outside [%s, %s]", name, ts, start, end)
		}
	}

	same := ps.Variables["same"].AsString()
	if same != "2026-06-15T00:00:00Z" {
		t.Errorf("same start/end: got %q want 2026-06-15T00:00:00Z", same)
	}
}

func TestParseExtendedDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"1h", time.Hour},
		{"30m", 30 * time.Minute},
		{"500ms", 500 * time.Millisecond},
		{"1d", 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"2w", 14 * 24 * time.Hour},
		{"0.5d", 12 * time.Hour},
	}
	for _, c := range cases {
		got, err := parseExtendedDuration(c.in)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q: got %s want %s", c.in, got, c.want)
		}
	}

	// Invalid
	for _, bad := range []string{"", "abc", "1x"} {
		if _, err := parseExtendedDuration(bad); err == nil {
			t.Errorf("%q: expected error, got nil", bad)
		}
	}
}

func TestEnvFunctionInProviderConfig(t *testing.T) {
	t.Setenv("TEST_DSN", "postgres://from-env:5432/db")

	dir := t.TempDir()
	path := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "s" {
  required_providers { postgres = { source = "builtin/postgres" } }
  provider "postgres" {
    dsn = env("TEST_DSN")
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ps, err := LoadScenario(path, "", nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	dsn := ps.ProviderConfigs["postgres"]["dsn"].AsString()
	if !strings.Contains(dsn, "from-env") {
		t.Errorf("env() in provider config: got %q", dsn)
	}
}
