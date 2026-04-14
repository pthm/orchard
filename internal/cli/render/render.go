// Package render holds the semantic styles orchard uses for all
// user-facing terminal output. Call sites reference styles by what
// they mean ("Success", "Error", "Muted") rather than by color
// names, so the palette can evolve without touching every cmd/.
//
// Styles are lipgloss values. Their no-op fallback on non-TTY
// output (via termenv auto-detection) means the same helpers work
// in pipes, CI, and plain terminals without a separate code path.
// Respecting NO_COLOR is automatic.
package render

import "github.com/charmbracelet/lipgloss"

// Color palette. Hex values are TrueColor; lipgloss degrades them
// to 256-color / 16-color / none based on the terminal's reported
// profile. Tweak here, affect everywhere.
var (
	colorSuccess = lipgloss.Color("#22c55e") // green
	colorError   = lipgloss.Color("#ef4444") // red
	colorWarn    = lipgloss.Color("#f59e0b") // amber
	colorLabel   = lipgloss.Color("#3b82f6") // blue
	colorKind    = lipgloss.Color("#a855f7") // violet
	colorAccent  = lipgloss.Color("#06b6d4") // cyan
	colorMuted   = lipgloss.Color("240")     // 256-color gray
)

// Semantic styles. Each is a lipgloss.Style value; call .Render to
// apply, or use as-is in text templates.
var (
	// Success marks a completed node or a "plan is valid" summary.
	Success = lipgloss.NewStyle().Foreground(colorSuccess).Bold(true)

	// SuccessMark is the ✓ glyph styled for a completed action.
	SuccessMark = lipgloss.NewStyle().Foreground(colorSuccess)

	// Error marks a failed node or a plan-time validation error.
	Error = lipgloss.NewStyle().Foreground(colorError).Bold(true)

	// ErrorMark is the ✗ glyph styled for a failed action.
	ErrorMark = lipgloss.NewStyle().Foreground(colorError)

	// Warn is for non-fatal diagnostics — teardown warnings, etc.
	Warn = lipgloss.NewStyle().Foreground(colorWarn)

	// Header styles a top-level section heading (Scenario, Plan).
	Header = lipgloss.NewStyle().Foreground(colorLabel).Bold(true)

	// Label styles a minor heading inside a section (Variables,
	// Providers, Execution plan, Outputs).
	Label = lipgloss.NewStyle().Foreground(colorLabel).Bold(true)

	// Kind styles node-type badges ([action], [component]) and
	// provider source indicators.
	Kind = lipgloss.NewStyle().Foreground(colorKind)

	// Ref styles references to other nodes (dependencies, source-
	// of-output links).
	Ref = lipgloss.NewStyle().Foreground(colorAccent)

	// Muted styles low-priority text: timings, node IDs in summary
	// lines, dim dependency arrows, file coords.
	Muted = lipgloss.NewStyle().Foreground(colorMuted)

	// Value styles scenario variable values, action output values.
	Value = lipgloss.NewStyle().Foreground(colorSuccess)

	// Bold is a plain bold with default foreground — for emphasis
	// that should not take on a semantic color.
	Bold = lipgloss.NewStyle().Bold(true)
)
