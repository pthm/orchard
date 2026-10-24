package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/cli/planview"
	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/snapshot"
)

var (
	planScenarioFlag   string
	planShowValuesFlag bool
)

func newPlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan <scenario>",
		Short: "Validate a scenario and show its execution plan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ps, err := parser.LoadScenario(args[0], planScenarioFlag, varFlags, seedOption(cmd)...)
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
			eng.SetKeepPlannedAttrs(planShowValuesFlag)

			// Surface provider-config typos at plan time — run catches
			// these too, but only after Configure has already opened
			// connections. PlanConfigure does the same schema-only
			// check, and hands database providers their schema
			// snapshots so typed actions can be checked offline.
			snaps, err := loadSnapshots(args[0], reg.Names())
			if err != nil {
				return err
			}
			if err := reg.PlanConfigure(cmd.Context(), ps.ProviderConfigs, snaps); err != nil {
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

			var valuesOf planview.ValuesOf
			if planShowValuesFlag {
				valuesOf = func(n *graph.Node) map[string]cty.Value { return eng.PlannedAttrs(n.ID) }
			}

			out := cmd.OutOrStdout()
			planview.Render(out, ps, g, providerOf, valuesOf)
			planview.RenderSuccess(out)
			if planShowValuesFlag {
				// Random values shown above come from this seed; run
				// with it to get the same ones.
				fmt.Fprintf(cmd.ErrOrStderr(), "seed: %d\n", ps.Seed)
			}
			for _, h := range eng.Hints() {
				fmt.Fprintf(cmd.ErrOrStderr(), "hint: %s\n", h)
			}
			for _, alias := range eng.UncheckedProviders() {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"note: no schema snapshot for provider %q, so its typed actions weren't checked against the database; run \"orchard schema pull %s\" to check them here\n",
					alias, args[0])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&planScenarioFlag, "scenario", "",
		"Pick a scenario by name when the file defines multiple")
	addSeedFlag(cmd)
	cmd.Flags().BoolVar(&planShowValuesFlag, "show-values", false,
		"List each action's evaluated attributes; values from other actions show as (known after run)")
	return cmd
}

// loadSnapshots reads the schema snapshot for each provider alias from
// the project the scenario belongs to (the nearest .orchard/ above
// it). Aliases without a snapshot are left out.
func loadSnapshots(scenario string, aliases []string) (map[string]*snapshot.Snapshot, error) {
	snaps := map[string]*snapshot.Snapshot{}
	project, ok := snapshot.FindProject(scenario)
	if !ok {
		return snaps, nil
	}
	for _, alias := range aliases {
		snap, err := snapshot.Read(snapshot.Path(project, alias))
		if errors.Is(err, snapshot.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		snaps[alias] = snap
	}
	return snaps, nil
}
