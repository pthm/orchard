package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pthm/orchard/internal/language"
)

var functionsJSONFlag bool

func newFunctionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "functions",
		Short: "List available HCL functions",
		Long: `Print the complete function reference. Default output is a
human-readable Markdown table grouped by category. Pass --json for
a machine-readable manifest suitable for editor grammars, LSP
servers, and docs generators.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if functionsJSONFlag {
				return printFunctionsJSON(cmd)
			}
			return printFunctionsMarkdown(cmd)
		},
	}
	cmd.Flags().BoolVar(&functionsJSONFlag, "json", false,
		"Output as JSON (for editor tooling and docs generators)")
	return cmd
}

func printFunctionsJSON(cmd *cobra.Command) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(language.All())
}

func printFunctionsMarkdown(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()

	for _, cat := range language.Categories() {
		fns := language.ByCategory(cat)
		fmt.Fprintf(out, "## %s\n\n", capitalise(cat))
		fmt.Fprintln(out, "| Function | Returns | Description |")
		fmt.Fprintln(out, "|----------|---------|-------------|")
		for _, fn := range fns {
			sig := formatSignature(fn)
			fmt.Fprintf(out, "| `%s` | `%s` | %s |\n",
				sig, fn.ReturnType, fn.Description)
		}
		fmt.Fprintln(out)
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
