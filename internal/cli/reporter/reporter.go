// Package reporter holds the engine.Reporter implementations the
// CLI picks between at startup. There are two: PlainReporter for
// non-TTY use (CI, pipes, `NO_COLOR`, `--no-color`) and
// TTYReporter for interactive shells. The choice is made by
// Select and returned as the opaque engine.Reporter interface.
package reporter

import (
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/cli/render"
	"github.com/pthm/orchard/internal/engine"
)

// Options controls reporter selection. Zero value selects
// automatically from the environment.
type Options struct {
	// Out is the writer for user-visible output. Defaults to os.Stdout.
	Out io.Writer

	// ForcePlain forces PlainReporter regardless of TTY / env.
	// Wired to the --no-color CLI flag.
	ForcePlain bool
}

// Select returns a reporter appropriate for the current environment.
// TTYReporter when Out is a terminal and neither NO_COLOR nor
// ForcePlain is set; PlainReporter otherwise.
func Select(opts Options) engine.Reporter {
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	if opts.ForcePlain || !isTerminal(out) || os.Getenv("NO_COLOR") != "" {
		return &PlainReporter{Out: out}
	}
	return &TTYReporter{Out: out}
}

// isTerminal returns true when w is writable to a TTY. Non-file
// writers (bytes.Buffer in tests, io.MultiWriter) are treated as
// non-TTY.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd())
}

// formatDuration renders a duration compactly: 3ms / 123ms / 1.2s / 1m3s.
// Sub-millisecond durations round up to 1ms so "0ms" never appears.
func formatDuration(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return "1ms"
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		m := int(d / time.Minute)
		s := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm%ds", m, s)
	}
}

// formatValue renders a scenario output value for display. Strings
// lose their quotes (they're already obviously strings), numbers
// print as integers when possible, bools as true/false, nulls as
// "null". Complex values round-trip through cty's GoString.
func formatValue(v cty.Value) string {
	if v.IsNull() {
		return "null"
	}
	switch v.Type() {
	case cty.String:
		return v.AsString()
	case cty.Number:
		bf := v.AsBigFloat()
		if bf.IsInt() {
			i, _ := bf.Int64()
			return fmt.Sprintf("%d", i)
		}
		f, _ := bf.Float64()
		return fmt.Sprintf("%g", f)
	case cty.Bool:
		if v.True() {
			return "true"
		}
		return "false"
	default:
		return v.GoString()
	}
}

// sortedKeys returns m's keys in ascending order. Ensures output
// is deterministic run-to-run regardless of Go map iteration.
func sortedKeys(m map[string]cty.Value) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PlainReporter prints machine-friendly lines with no color or
// alignment. Used when stdout is a pipe, in CI, when NO_COLOR is
// set, or when --no-color is explicit. Output is intended to be
// greppable and stable across runs.
type PlainReporter struct {
	Out io.Writer
}

// RunStarted implements engine.Reporter.
func (r *PlainReporter) RunStarted(scenario string) {
	fmt.Fprintf(r.Out, "scenario: %s\n", scenario)
	fmt.Fprintln(r.Out, "executing...")
}

// NodeStarted implements engine.Reporter. Plain reporter stays
// silent on start — the completion line carries the timing and
// is enough signal for log consumers.
func (r *PlainReporter) NodeStarted(string) {}

// NodeCompleted implements engine.Reporter.
func (r *PlainReporter) NodeCompleted(nodeID string, d time.Duration) {
	fmt.Fprintf(r.Out, "  ok %s (%s)\n", nodeID, formatDuration(d))
}

// NodeFailed implements engine.Reporter.
func (r *PlainReporter) NodeFailed(nodeID string, err error) {
	fmt.Fprintf(r.Out, "  fail %s: %v\n", nodeID, err)
}

// RunCompleted implements engine.Reporter.
func (r *PlainReporter) RunCompleted(s engine.RunSummary) {
	if !s.OK {
		fmt.Fprintf(r.Out, "failed after %s (%d ok, %d failed)\n",
			formatDuration(s.Duration), s.Succeeded, s.Failed)
		return
	}
	if len(s.Outputs) > 0 {
		fmt.Fprintln(r.Out, "\noutputs:")
		for _, name := range sortedKeys(s.Outputs) {
			fmt.Fprintf(r.Out, "  %s = %s\n", name, formatValue(s.Outputs[name]))
		}
	}
	fmt.Fprintf(r.Out, "\ndone in %s (%d nodes).\n",
		formatDuration(s.Duration), s.Succeeded)
}

// TTYReporter produces styled output for interactive shells.
// Uses lipgloss via internal/cli/render; auto-degrades on
// terminals that don't support color via termenv.
type TTYReporter struct {
	Out io.Writer
}

// RunStarted implements engine.Reporter.
func (r *TTYReporter) RunStarted(scenario string) {
	fmt.Fprintln(r.Out,
		render.Header.Render("▸ Scenario:")+" "+render.Bold.Render(scenario))
	fmt.Fprintln(r.Out)
}

// NodeStarted implements engine.Reporter. Currently silent —
// adding a pre-completion line is where live-progress would hook
// in; deferred for now.
func (r *TTYReporter) NodeStarted(string) {}

// NodeCompleted implements engine.Reporter.
func (r *TTYReporter) NodeCompleted(nodeID string, d time.Duration) {
	fmt.Fprintf(r.Out, "  %s %s  %s\n",
		render.SuccessMark.Render("✓"),
		nodeID,
		render.Muted.Render("("+formatDuration(d)+")"))
}

// NodeFailed implements engine.Reporter.
func (r *TTYReporter) NodeFailed(nodeID string, err error) {
	fmt.Fprintf(r.Out, "  %s %s\n     %s\n",
		render.ErrorMark.Render("✗"),
		render.Bold.Render(nodeID),
		render.Error.Render(err.Error()))
}

// RunCompleted implements engine.Reporter.
func (r *TTYReporter) RunCompleted(s engine.RunSummary) {
	if !s.OK {
		fmt.Fprintf(r.Out, "\n%s  %s\n",
			render.Error.Render("✗ Failed"),
			render.Muted.Render(fmt.Sprintf("after %s  (%d ok, %d failed)",
				formatDuration(s.Duration), s.Succeeded, s.Failed)))
		return
	}
	if len(s.Outputs) > 0 {
		fmt.Fprintln(r.Out)
		fmt.Fprintln(r.Out, render.Label.Render("Outputs"))
		names := sortedKeys(s.Outputs)
		width := longestName(names)
		for _, name := range names {
			fmt.Fprintf(r.Out, "  %s %s %s\n",
				padRight(name, width),
				render.Muted.Render("="),
				render.Value.Render(formatValue(s.Outputs[name])))
		}
	}
	fmt.Fprintf(r.Out, "\n%s  %s\n",
		render.Success.Render("✓ Done"),
		render.Muted.Render(fmt.Sprintf("in %s  (%d nodes)",
			formatDuration(s.Duration), s.Succeeded)))
}

func longestName(names []string) int {
	n := 0
	for _, s := range names {
		if len(s) > n {
			n = len(s)
		}
	}
	return n
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + spaces(width-len(s))
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = ' '
	}
	return string(out)
}
