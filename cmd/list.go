package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/spf13/cobra"
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

			scenarios, err := findScenarios(dir)
			if err != nil {
				return err
			}

			if len(scenarios) == 0 {
				fmt.Println("No scenarios found.")
				return nil
			}

			fmt.Println("Available scenarios:")
			for _, s := range scenarios {
				fmt.Printf("  %s  (%s)\n", s.name, s.path)
			}
			return nil
		},
	}
}

type scenarioInfo struct {
	name string
	path string
}

func findScenarios(dir string) ([]scenarioInfo, error) {
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
			scenarios = append(scenarios, scenarioInfo{
				name: block.Labels[0],
				path: path,
			})
		}
		return nil
	})

	return scenarios, err
}
