package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pthm/orchard/internal/cli/planview"
	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
)

var planScenarioFlag string

func newPlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan <scenario>",
		Short: "Validate a scenario and show its execution plan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ps, err := parser.LoadScenario(args[0], planScenarioFlag, varFlags)
			if err != nil {
				return fmt.Errorf("loading scenario: %w", err)
			}

			g, err := graph.Build(ps)
			if err != nil {
				return fmt.Errorf("building graph: %w", err)
			}

			// Register providers for schemas only; do not Configure.
			// Plan must not open database connections or make external
			// calls — it is a read-only type-checking pass. External
			// providers DO start subprocesses during Register (the
			// describe handshake reads the schema), so Close still
			// matters — defer it immediately.
			reg, err := registerProviders(ps, providerModePlan)
			if err != nil {
				return fmt.Errorf("registering providers: %w", err)
			}
			defer func() {
				if closeErr := reg.Close(); closeErr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", closeErr)
				}
			}()

			eng, err := engine.New(reg)
			if err != nil {
				return fmt.Errorf("building engine: %w", err)
			}

			// Surface provider-config typos at plan time — run catches
			// these too, but only after Configure has already opened
			// connections. Plan is the right layer for the cheap
			// schema-only check.
			if err := reg.ValidateConfigs(ps.ProviderConfigs); err != nil {
				return fmt.Errorf("validation failed: %w", err)
			}
			if err := eng.ValidateGraph(g); err != nil {
				return fmt.Errorf("validation failed: %w", err)
			}
			if err := eng.ValidateReferences(cmd.Context(), ps, g); err != nil {
				return fmt.Errorf("validation failed: %w", err)
			}

			// Provide a route resolver so each action header can name
			// the provider that will handle it. Falls back to empty
			// string when the routing is unresolved, which keeps the
			// renderer tolerant of validation gaps.
			providerOf := func(n *graph.Node) string {
				if n.Kind != graph.ActionNode {
					return ""
				}
				alias, _ := eng.ResolveProvider(n.ActionType, n.ProviderAlias)
				return alias
			}

			out := cmd.OutOrStdout()
			planview.Render(out, ps, g, providerOf)
			planview.RenderSuccess(out)
			return nil
		},
	}
	cmd.Flags().StringVar(&planScenarioFlag, "scenario", "",
		"Pick a scenario by name when the file defines multiple")
	return cmd
}
