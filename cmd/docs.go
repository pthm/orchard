package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pthm/orchard/internal/docs"
)

// newDocsCmd builds the `orchard docs <source>` subcommand. It
// resolves the source string the same way scenarios do (builtin or
// exec), asks the provider for its schema, and writes a markdown
// reference page to stdout.
//
// The command uses providerModePlan so external providers are
// consulted via their `schema` subcommand (short-lived probe) rather
// than spawned as full subprocesses. Doc generation is a
// read-only metadata operation; there is no reason to pay the cost
// of a long-lived process or require credentials just to render a
// reference page.
func newDocsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "docs <provider-source>",
		Short: "Render provider reference documentation as Markdown",
		Long: `Render a provider's reference documentation from its schema.

Accepts any provider source a scenario could declare:

  orchard docs builtin/postgres
  orchard docs exec:./bin/orchard-stripe

Output goes to stdout. Intended for pipelines like:

  orchard docs builtin/postgres > docs/content-generated/providers/postgres.md`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source := args[0]

			p, err := instantiateProvider(source, providerModePlan)
			if err != nil {
				return fmt.Errorf("resolve %q: %w", source, err)
			}
			defer func() { _ = p.Close() }()

			out := docs.Render(p.Schema(), source)
			if _, err := fmt.Fprint(cmd.OutOrStdout(), out); err != nil {
				return err
			}
			return nil
		},
	}
}
