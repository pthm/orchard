package reporter

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/engine"
)

func TestPlainReporterLifecycle(t *testing.T) {
	var buf bytes.Buffer
	r := &PlainReporter{Out: &buf}
	r.RunStarted("seed_dev")
	r.NodeStarted("action.sql.x") // silent
	r.NodeCompleted("action.sql.x", nil, 12*time.Millisecond)
	r.NodeCompleted("action.sql.y", nil, 3*time.Second)
	r.RunCompleted(engine.RunSummary{
		Scenario:  "seed_dev",
		Outputs:   map[string]cty.Value{"id": cty.NumberIntVal(42)},
		Duration:  3*time.Second + 20*time.Millisecond,
		Succeeded: 2,
		OK:        true,
	})

	out := buf.String()
	for _, want := range []string{
		"scenario: seed_dev",
		"ok action.sql.x (12ms)",
		"ok action.sql.y (3.0s)",
		"outputs:",
		"id = 42",
		"done in 3.0s (2 nodes)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("PlainReporter output missing %q\n---\n%s", want, out)
		}
	}
}

func TestPlainReporterFailure(t *testing.T) {
	var buf bytes.Buffer
	r := &PlainReporter{Out: &buf}
	r.RunStarted("broken")
	r.NodeFailed("action.sql.x", errors.New("boom"))
	r.RunCompleted(engine.RunSummary{
		Scenario:  "broken",
		Duration:  50 * time.Millisecond,
		Succeeded: 0,
		Failed:    1,
		OK:        false,
		Err:       errors.New("boom"),
	})
	out := buf.String()
	if !strings.Contains(out, "fail action.sql.x: boom") {
		t.Errorf("expected fail line, got %q", out)
	}
	if !strings.Contains(out, "failed after 50ms") {
		t.Errorf("expected failure summary, got %q", out)
	}
}

func TestTTYReporterEmitsKeyContent(t *testing.T) {
	// Even without real ANSI escapes (lipgloss auto-degrades on a
	// non-TTY writer), the literal text content should be present.
	var buf bytes.Buffer
	r := &TTYReporter{Out: &buf}
	r.RunStarted("seed_dev")
	r.NodeCompleted("action.sql.x", nil, 12*time.Millisecond)
	r.RunCompleted(engine.RunSummary{
		Scenario:  "seed_dev",
		Outputs:   map[string]cty.Value{"id": cty.NumberIntVal(42)},
		Duration:  20 * time.Millisecond,
		Succeeded: 1,
		OK:        true,
	})
	out := buf.String()
	for _, want := range []string{
		"seed_dev",
		"action.sql.x",
		"12ms",
		"Outputs",
		"id",
		"42",
		"Done",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("TTYReporter output missing %q\n---\n%s", want, out)
		}
	}
}

func TestSelectNoColorForcesPlain(t *testing.T) {
	var buf bytes.Buffer
	r := Select(Options{Out: &buf, ForcePlain: true})
	if _, ok := r.(*PlainReporter); !ok {
		t.Fatalf("ForcePlain should yield PlainReporter, got %T", r)
	}
}

func TestSelectNonTTYDefaultsToPlain(t *testing.T) {
	// A bytes.Buffer is never a TTY.
	var buf bytes.Buffer
	r := Select(Options{Out: &buf})
	if _, ok := r.(*PlainReporter); !ok {
		t.Fatalf("non-TTY should yield PlainReporter, got %T", r)
	}
}

func TestFormatDurationBuckets(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Microsecond, "1ms"},
		{3 * time.Millisecond, "3ms"},
		{123 * time.Millisecond, "123ms"},
		{1200 * time.Millisecond, "1.2s"},
		{3 * time.Second, "3.0s"},
		{65 * time.Second, "1m5s"},
	}
	for _, c := range cases {
		if got := formatDuration(c.d); got != c.want {
			t.Errorf("formatDuration(%s): got %q want %q", c.d, got, c.want)
		}
	}
}
