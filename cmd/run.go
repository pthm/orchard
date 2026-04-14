package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pthm/orchard/internal/cli/reporter"
	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"

	// Side-effect imports register builtin provider sources. Adding
	// a new builtin provider is a one-line change here.
	_ "github.com/pthm/orchard/pkg/provider/exec"
	_ "github.com/pthm/orchard/pkg/provider/http"
	_ "github.com/pthm/orchard/pkg/provider/postgres"
)

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <scenario>",
		Short: "Execute a scenario",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			ps, err := parser.LoadScenario(args[0], varFlags)
			if err != nil {
				return fmt.Errorf("loading scenario: %w", err)
			}

			g, err := graph.Build(ps)
			if err != nil {
				return fmt.Errorf("building graph: %w", err)
			}

			// Register first, defer Close, then Configure. This order
			// matters: Spawn on an exec: source launches a subprocess
			// during Register, and Configure on any provider can
			// fail. If we configured before deferring Close, a
			// Configure error would leak the already-started
			// providers.
			reg, err := registerProviders(ps)
			if err != nil {
				return fmt.Errorf("registering providers: %w", err)
			}
			defer func() {
				if closeErr := reg.Close(); closeErr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", closeErr)
				}
			}()

			if err := reg.Configure(ctx, ps.ProviderConfigs); err != nil {
				return fmt.Errorf("configuring providers: %w", err)
			}

			eng, err := engine.New(reg)
			if err != nil {
				return fmt.Errorf("building engine: %w", err)
			}

			// Fail fast on structural and reference errors before the
			// providers do any real work. These are the same checks
			// `orchard plan` performs, so a valid plan run here is a
			// no-op but catches authoring mistakes cheaply when the
			// user skipped plan.
			if err := eng.ValidateGraph(g); err != nil {
				return fmt.Errorf("validation failed: %w", err)
			}
			if err := eng.ValidateReferences(ps, g); err != nil {
				return fmt.Errorf("validation failed: %w", err)
			}

			eng.SetReporter(reporter.Select(reporter.Options{
				Out:        cmd.OutOrStdout(),
				ForcePlain: noColorFlag,
			}))

			if _, err := eng.Execute(ctx, ps, g); err != nil {
				return err
			}
			return nil
		},
	}
}
