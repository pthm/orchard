package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/spf13/cobra"

	"github.com/pthm/orchard/internal/cli/render"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
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
				fmt.Fprintf(out, "  %s  %s\n",
					render.Bold.Render(s.name),
					render.Muted.Render("("+s.path+")"),
				)
				if s.description != "" {
					fmt.Fprintf(out, "  %s\n",
						render.Muted.Render(s.description))
				}
				if len(s.tags) > 0 {
					var styled []string
					for _, tag := range s.tags {
						styled = append(styled, render.Kind.Render(tag))
					}
					fmt.Fprintf(out, "  %s\n", strings.Join(styled, " "))
				}
			}
			return nil
		},
	}
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
