package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/spf13/cobra"

	"github.com/pthm/orchard/internal/cli/render"
	"github.com/pthm/orchard/internal/record"
)

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list [directory]",
		Short: "List available scenarios",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}
			out := cmd.OutOrStdout()

			scenarios, err := findScenarios(dir, cmd.ErrOrStderr())
			if err != nil {
				return err
			}

			if len(scenarios) == 0 {
				fmt.Fprintln(out, "No scenarios found.")
				return nil
			}

			for _, s := range scenarios {
				line := render.Bold.Render(s.name)
				if len(s.tags) > 0 {
					line += "  " + render.Kind.Render("["+strings.Join(s.tags, ", ")+"]")
				}
				line += "  " + render.Muted.Render("("+s.path+")")
				fmt.Fprintf(out, "  %s\n", line)
				if s.description != "" {
					fmt.Fprintf(out, "  %s\n",
						render.Muted.Render(s.description))
				}
			}
			return nil
		},
	}
	cmd.AddCommand(newListRunsCmd())
	return cmd
}

// newListRunsCmd builds `orchard list runs`: one line per run record
// in --record-dir, newest first.
func newListRunsCmd() *cobra.Command {
	var recordDir string
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "List recorded runs, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			files, err := record.List(recordDir, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if len(files) == 0 {
				fmt.Fprintf(out, "No runs found in %s (see --record-dir)\n", recordDir)
				return nil
			}
			style := func(s lipgloss.Style, text string) string {
				if noColorFlag {
					return text
				}
				return s.Render(text)
			}
			for _, f := range files {
				rec := f.Record
				status := style(render.Success, rec.Status)
				if rec.Status != record.StatusSucceeded {
					status = style(render.Error, rec.Status)
				}
				line := rec.ID + "  " + style(render.Bold, rec.Scenario.Name) + "  " + status
				if marker := tornDownMarker(rec); marker != "" {
					line += "  " + style(render.Warn, marker)
				}
				line += "  " + style(render.Muted, rec.StartedAt.UTC().Format(time.RFC3339)+"  "+nodeCounts(rec))
				fmt.Fprintf(out, "  %s\n", line)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&recordDir, "record-dir", ".",
		"Directory to read run records from")
	return cmd
}

// tornDownMarker says how far `orchard teardown` got with rec.
func tornDownMarker(rec *record.Record) string {
	if rec.TornDownAt != nil {
		return "torn down"
	}
	for _, n := range rec.Nodes {
		if n.TornDown {
			return "partly torn down"
		}
	}
	return ""
}

// nodeCounts summarises rec's nodes by status, e.g. "1 succeeded, 1 failed".
func nodeCounts(rec *record.Record) string {
	counts := map[string]int{}
	for _, n := range rec.Nodes {
		counts[n.Status]++
	}
	var parts []string
	for _, st := range []string{record.StatusSucceeded, record.StatusFailed, record.StatusSkipped} {
		if counts[st] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[st], st))
		}
	}
	if len(parts) == 0 {
		return "no nodes"
	}
	return strings.Join(parts, ", ")
}

type scenarioInfo struct {
	name        string
	path        string
	description string
	tags        []string
}

func findScenarios(dir string, warn io.Writer) ([]scenarioInfo, error) {
	var scenarios []scenarioInfo
	p := hclparse.NewParser()

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && info.Name() == ".seed" {
			return filepath.SkipDir
		}
		if !strings.HasSuffix(path, ".hcl") {
			return nil
		}

		file, diags := p.ParseHCLFile(path)
		if diags.HasErrors() {
			fmt.Fprintf(warn, "warning: skipping %s: %s\n", path, diags.Error())
			return nil
		}

		content, _, diags := file.Body.PartialContent(&hcl.BodySchema{
			Blocks: []hcl.BlockHeaderSchema{
				{Type: "scenario", LabelNames: []string{"name"}},
			},
		})
		if diags.HasErrors() || len(content.Blocks) == 0 {
			return nil
		}

		for _, block := range content.Blocks {
			info := scenarioInfo{
				name: block.Labels[0],
				path: path,
			}
			attrs, _, ad := block.Body.PartialContent(&hcl.BodySchema{
				Attributes: []hcl.AttributeSchema{
					{Name: "description"},
					{Name: "tags"},
				},
			})
			if !ad.HasErrors() {
				if a, ok := attrs.Attributes["description"]; ok {
					if v, vd := a.Expr.Value(nil); !vd.HasErrors() && v.Type().FriendlyName() == "string" {
						info.description = v.AsString()
					}
				}
				if a, ok := attrs.Attributes["tags"]; ok {
					if v, vd := a.Expr.Value(nil); !vd.HasErrors() {
						it := v.ElementIterator()
						for it.Next() {
							_, el := it.Element()
							if el.Type().FriendlyName() == "string" {
								info.tags = append(info.tags, el.AsString())
							}
						}
					}
				}
			}
			scenarios = append(scenarios, info)
		}
		return nil
	})

	return scenarios, err
}
