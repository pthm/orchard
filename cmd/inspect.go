package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/pthm/orchard/internal/cli/render"
	"github.com/pthm/orchard/internal/record"
)

// newInspectCmd builds `orchard inspect`: everything one run record
// says, in a readable layout.
func newInspectCmd() *cobra.Command {
	var recordDir string
	cmd := &cobra.Command{
		Use:   "inspect <run-id | record-file>",
		Short: "Show the details of a recorded run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := record.Resolve(recordDir, args[0], cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			rec := f.Record
			style := func(s lipgloss.Style, text string) string {
				if noColorFlag {
					return text
				}
				return s.Render(text)
			}
			status := func(st string) string {
				if st == record.StatusSucceeded {
					return style(render.Success, st)
				}
				return style(render.Error, st)
			}

			fmt.Fprintf(out, "  ID          %s\n", rec.ID)
			fmt.Fprintf(out, "  Scenario    %s  %s\n", style(render.Bold, rec.Scenario.Name), style(render.Muted, "("+rec.Scenario.Path+")"))
			fmt.Fprintf(out, "  Status      %s\n", status(rec.Status))
			fmt.Fprintf(out, "  Started     %s\n", rec.StartedAt.UTC().Format(time.RFC3339))
			fmt.Fprintf(out, "  Duration    %s\n", rec.FinishedAt.Sub(rec.StartedAt).Round(time.Millisecond))
			if rec.TornDownAt != nil {
				fmt.Fprintf(out, "  Torn down   %s\n", rec.TornDownAt.UTC().Format(time.RFC3339))
			}
			if rec.Error != "" {
				fmt.Fprintf(out, "  Error       %s\n", style(render.Error, rec.Error))
			}

			if len(rec.Variables) > 0 {
				fmt.Fprintf(out, "\n  %s\n", style(render.Bold, "Variables"))
				printValues(out, rec.Variables)
			}

			fmt.Fprintf(out, "\n  %s\n", style(render.Bold, "Nodes"))
			for _, n := range rec.Nodes {
				line := status(n.Status) + "  " + n.ID + "  " + style(render.Muted, n.FinishedAt.Sub(n.StartedAt).Round(time.Millisecond).String())
				if n.TornDown {
					line += "  " + style(render.Warn, "torn down")
				}
				fmt.Fprintf(out, "    %s\n", line)
				if n.Error != "" {
					fmt.Fprintf(out, "      error: %s\n", style(render.Error, n.Error))
				}
				if n.Undo != nil {
					attrs, err := json.Marshal(n.Undo.Attrs)
					if err != nil {
						return err
					}
					fmt.Fprintf(out, "      undo: %s %s\n", n.Undo.Action, attrs)
				}
			}
			if rec.Status == record.StatusFailed {
				fmt.Fprintf(out, "    %s\n", style(render.Muted, "Nodes not listed did not run."))
			}

			if len(rec.Outputs) > 0 {
				fmt.Fprintf(out, "\n  %s\n", style(render.Bold, "Outputs"))
				printValues(out, rec.Outputs)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&recordDir, "record-dir", ".",
		"Directory to look a run ID up in")
	return cmd
}

// printValues prints a record's name → JSON value map sorted by name,
// each value compacted onto one line.
func printValues(out io.Writer, values map[string]json.RawMessage) {
	for _, k := range slices.Sorted(maps.Keys(values)) {
		var buf bytes.Buffer
		if err := json.Compact(&buf, values[k]); err != nil {
			buf.Reset()
			buf.Write(values[k])
		}
		fmt.Fprintf(out, "    %s = %s\n", k, buf.String())
	}
}
