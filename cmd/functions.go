package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pthm/orchard/internal/language"
)

var (
	functionsJSONFlag bool
	functionsHugoFlag bool
)

func newFunctionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "functions",
		Short: "List available HCL functions",
		Long: `Print the complete function reference. Default output is a
human-readable Markdown table grouped by category. Pass --json for
a machine-readable manifest suitable for editor grammars, LSP
servers, and docs generators. Pass --hugo to emit a full page with
frontmatter for the docs site.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if functionsJSONFlag {
				return printFunctionsJSON(cmd)
			}
			return printFunctionsMarkdown(cmd, functionsHugoFlag)
		},
	}
	cmd.Flags().BoolVar(&functionsJSONFlag, "json", false,
		"Output as JSON (for editor tooling and docs generators)")
	cmd.Flags().BoolVar(&functionsHugoFlag, "hugo", false,
		"Include Hugo frontmatter and expanded detail for docs site generation")
	return cmd
}

func printFunctionsJSON(cmd *cobra.Command) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(language.All())
}

func printFunctionsMarkdown(cmd *cobra.Command, hugo bool) error {
	out := cmd.OutOrStdout()

	if hugo {
		fmt.Fprintln(out, "---")
		fmt.Fprintln(out, "title: Functions")
		fmt.Fprintln(out, "weight: 30")
		fmt.Fprintln(out, "---")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "# Functions")
		fmt.Fprintln(out)
		fmt.Fprintf(out, "Orchard exposes %d functions in every expression — variable defaults,\n", len(language.All()))
		fmt.Fprintln(out, "provider configs, component inputs, action attributes, scenario outputs,")
		fmt.Fprintln(out, "component outputs, and teardown bodies. All names use lowercase compound")
		fmt.Fprintln(out, "words with no separators (`jsonencode`, `timeadd`, `timeseries`, ...).")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Functions marked with **(impure)** may return different values on each")
		fmt.Fprintln(out, "call. That's intentional — scenarios are single-shot imperative runs,")
		fmt.Fprintln(out, "not convergent plans.")
		fmt.Fprintln(out)
	}

	for _, cat := range language.Categories() {
		fns := language.ByCategory(cat)
		fmt.Fprintf(out, "## %s\n\n", capitalise(cat))

		if hugo {
			// Expanded format: one subsection per function with
			// signature, description, detail, and examples.
			for _, fn := range fns {
				purity := ""
				if !fn.Pure {
					purity = " **(impure)**"
				}
				fmt.Fprintf(out, "### `%s`%s\n\n", fn.Name, purity)
				fmt.Fprintf(out, "```\n%s → %s\n```\n\n", formatSignature(fn), fn.ReturnType)
				fmt.Fprintln(out, fn.Description)
				fmt.Fprintln(out)

				if fn.Detail != "" {
					fmt.Fprintln(out, fn.Detail)
					fmt.Fprintln(out)
				}

				if len(fn.Params) > 0 || fn.VarParam != nil {
					fmt.Fprintln(out, "| Parameter | Type | Description |")
					fmt.Fprintln(out, "|-----------|------|-------------|")
					for _, p := range fn.Params {
						desc := p.Description
						if desc == "" {
							desc = ""
						}
						fmt.Fprintf(out, "| `%s` | `%s` | %s |\n", p.Name, p.Type, desc)
					}
					if fn.VarParam != nil {
						desc := fn.VarParam.Description
						if desc == "" {
							desc = "Variadic."
						}
						fmt.Fprintf(out, "| `%s...` | `%s` | %s |\n", fn.VarParam.Name, fn.VarParam.Type, desc)
					}
					fmt.Fprintln(out)
				}

				for _, ex := range fn.Examples {
					if ex.Code != "" {
						fmt.Fprintf(out, "```hcl\n%s\n```\n", ex.Code)
						if ex.Result != "" {
							fmt.Fprintf(out, "→ `%s`\n", ex.Result)
						}
						fmt.Fprintln(out)
					}
				}
			}
		} else {
			// Compact table format for terminal output.
			fmt.Fprintln(out, "| Function | Returns | Description |")
			fmt.Fprintln(out, "|----------|---------|-------------|")
			for _, fn := range fns {
				sig := formatSignature(fn)
				fmt.Fprintf(out, "| `%s` | `%s` | %s |\n",
					sig, fn.ReturnType, fn.Description)
			}
			fmt.Fprintln(out)
		}
	}
	return nil
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func formatSignature(fn language.FunctionDef) string {
	var parts []string
	for _, p := range fn.Params {
		parts = append(parts, p.Name)
	}
	if fn.VarParam != nil {
		parts = append(parts, fn.VarParam.Name+"...")
	}
	return fn.Name + "(" + strings.Join(parts, ", ") + ")"
}
