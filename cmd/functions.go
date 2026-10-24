package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pthm/orchard/internal/language"
)

var (
	functionsJSONFlag    bool
	functionsHugoDirFlag string
)

func newFunctionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "functions",
		Short: "List available HCL functions",
		Long: `Print the complete function reference. Default output is a
human-readable Markdown table grouped by category. Pass --json for
a machine-readable manifest suitable for editor grammars, LSP
servers, and docs generators. Pass --hugo-dir to write the docs
site's function pages: an index and one page per category.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case functionsJSONFlag:
				return printFunctionsJSON(cmd)
			case functionsHugoDirFlag != "":
				return writeFunctionsHugo(functionsHugoDirFlag)
			}
			printFunctionsTable(cmd.OutOrStdout())
			return nil
		},
	}
	cmd.Flags().BoolVar(&functionsJSONFlag, "json", false,
		"Output as JSON (for editor tooling and docs generators)")
	cmd.Flags().StringVar(&functionsHugoDirFlag, "hugo-dir", "",
		"Write the docs site's function pages (an _index.md and one page per category) to this directory")
	return cmd
}

func printFunctionsJSON(cmd *cobra.Command) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(language.All())
}

// printFunctionsTable prints one compact table per category.
func printFunctionsTable(out io.Writer) {
	for _, cat := range language.Categories() {
		fmt.Fprintf(out, "## %s\n\n", capitalise(cat))
		fmt.Fprintln(out, "| Function | Returns | Description |")
		fmt.Fprintln(out, "|----------|---------|-------------|")
		for _, fn := range language.ByCategory(cat) {
			fmt.Fprintf(out, "| `%s` | `%s` | %s |\n", formatSignature(fn), fn.ReturnType, fn.Description)
		}
		fmt.Fprintln(out)
	}
}

// categoryIntros opens each category's page.
var categoryIntros = map[string]string{
	"collections": "Build, reshape and query lists, maps and objects.",
	"encoding":    "Encode and decode JSON, and decode CSV.",
	"environment": "Read the environment orchard runs in.",
	"fake":        "Generate realistic fake data — names, emails, addresses, companies, text and more — with [gofakeit](https://github.com/brianvoe/gofakeit). The [fake data kinds](../fake-kinds/) page lists every kind `fake()` accepts.",
	"identity":    "Generate unique IDs in the common formats. The time-ordered ones take an optional timestamp, to match a backdated `created_at`.",
	"math":        "Arithmetic on numbers.",
	"random":      "Draw random numbers, picks and strings.",
	"regex":       "Match and extract with regular expressions (RE2 syntax).",
	"sets":        "Combine and compare sets.",
	"strings":     "Format, split, join, trim and transform strings.",
	"time":        "Parse, shift and generate RFC 3339 timestamps.",
}

const seedNote = "Random values follow the run's seed, so a run can be repeated; see [random values and seeds](../../expressions/#random-values-and-seeds)."

// writeFunctionsHugo writes the docs site's function reference: an
// index page and one page per category, plus the fake data kinds.
func writeFunctionsHugo(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	write := func(name string, body func(io.Writer)) error {
		var b strings.Builder
		body(&b)
		return os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644)
	}

	cats := language.Categories()
	err := write("_index.md", func(out io.Writer) {
		fmt.Fprint(out, "---\ntitle: Functions\nweight: 30\n---\n\n")
		fmt.Fprintf(out, "Orchard exposes %d functions in every expression — variable defaults,\n", len(language.All()))
		fmt.Fprintln(out, "provider configs, component inputs, action attributes, scenario outputs,")
		fmt.Fprintln(out, "component outputs, and teardown bodies. All names use lowercase compound")
		fmt.Fprintln(out, "words with no separators (`jsonencode`, `timeadd`, `timeseries`, ...).")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Functions marked **impure** may return a different value on each call:")
		fmt.Fprintln(out, "random values, IDs, the clock and the environment. Random values follow")
		fmt.Fprintln(out, "the run's seed; see [random values and seeds](../expressions/#random-values-and-seeds).")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "| Group | Functions |")
		fmt.Fprintln(out, "|-------|-----------|")
		for _, cat := range cats {
			fns := language.ByCategory(cat)
			names := make([]string, 0, len(fns))
			for _, fn := range fns {
				names = append(names, fmt.Sprintf("[`%s`](%s/#%s)", fn.Name, cat, fn.Name))
			}
			fmt.Fprintf(out, "| [%s](%s/) | %s |\n", capitalise(cat), cat, strings.Join(names, ", "))
		}
		fmt.Fprintln(out)
	})
	if err != nil {
		return err
	}

	for i, cat := range cats {
		err := write(cat+".md", func(out io.Writer) {
			fmt.Fprintf(out, "---\ntitle: %s\nweight: %d\n---\n\n", capitalise(cat), (i+1)*10)
			if intro := categoryIntros[cat]; intro != "" {
				fmt.Fprintf(out, "%s\n\n", intro)
			}
			if cat == "random" || cat == "identity" || cat == "fake" {
				fmt.Fprintf(out, "%s\n\n", seedNote)
			}
			for _, fn := range language.ByCategory(cat) {
				printFunction(out, fn)
			}
		})
		if err != nil {
			return err
		}
		if cat == "fake" {
			// Right after the Fake page in the sidebar.
			if err := write("fake-kinds.md", func(out io.Writer) { printFakeKinds(out, (i+1)*10+5) }); err != nil {
				return err
			}
		}
	}
	return nil
}

// printFunction writes one function's section: heading (its anchor is
// the function's name), signature, description, parameters, examples.
func printFunction(out io.Writer, fn language.FunctionDef) {
	fmt.Fprintf(out, "## `%s` {#%s}\n\n", fn.Name, fn.Name)
	if !fn.Pure {
		// Under the heading, so the page's table of contents stays
		// just the names.
		fmt.Fprint(out, "{{< badge content=\"impure\" color=\"amber\" >}}\n\n")
	}
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
			fmt.Fprintf(out, "| `%s` | `%s` | %s |\n", p.Name, p.Type, p.Description)
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
		if ex.Code == "" {
			continue
		}
		fmt.Fprintf(out, "```hcl\n%s\n```\n", ex.Code)
		if ex.Result != "" {
			fmt.Fprintf(out, "→ `%s`\n", ex.Result)
		}
		fmt.Fprintln(out)
	}
}

// printFakeKinds writes the page listing fake()'s kinds by category,
// from gofakeit's own catalogue.
func printFakeKinds(out io.Writer, weight int) {
	kinds := language.FakeKinds()
	byCat := map[string][]language.FakeKind{}
	var cats []string
	for _, k := range kinds {
		if byCat[k.Category] == nil {
			cats = append(cats, k.Category)
		}
		byCat[k.Category] = append(byCat[k.Category], k)
	}
	sort.Strings(cats)
	fmt.Fprintf(out, "---\ntitle: Fake data kinds\nweight: %d\n---\n\n", weight)
	fmt.Fprintf(out, "The %d kinds [`fake()`](../fake/#fake) accepts, by category. Pass parameters as an object,\n", len(kinds))
	fmt.Fprintln(out, "`fake(\"number\", { min = 1, max = 9 })`; those with a default are optional.")
	fmt.Fprintln(out)
	for _, cat := range cats {
		fmt.Fprintf(out, "## %s\n\n", capitalise(cat))
		fmt.Fprintln(out, "| Kind | Description | Parameters |")
		fmt.Fprintln(out, "|------|-------------|------------|")
		for _, k := range byCat[cat] {
			var params []string
			for _, p := range k.Params {
				s := fmt.Sprintf("%#q (%s", p.Field, p.Type)
				if p.Default != "" {
					s += ", default `" + p.Default + "`"
				}
				params = append(params, s+")")
			}
			desc := strings.ReplaceAll(k.Description, "|", "\\|")
			fmt.Fprintf(out, "| `%s` | %s | %s |\n", k.Name, desc, strings.Join(params, ", "))
		}
		fmt.Fprintln(out)
	}
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
