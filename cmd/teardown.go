package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pthm/orchard/internal/cli/reporter"
	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/internal/record"
)

// newTeardownCmd builds the `orchard teardown <record-file>`
// subcommand. It reads a record produced by a prior `orchard run`,
// reloads the scenario the record points at, and invokes the
// engine's Teardown pass.
//
// The scenario must still be reachable at the path stored in the
// record (absolute paths are recorded at run time, so the record
// can be consumed from any working directory). The scenario's
// lifecycle { teardown { } } blocks drive the cleanup; actions
// without a teardown block are silently skipped.
//
// Unlike `orchard run`, variable overrides come from the record,
// not the --var flag — teardown is meant to clean up the state the
// recorded run produced. Future work may reintroduce overrides for
// partial / targeted teardown.
func newTeardownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "teardown <record-file>",
		Short: "Tear down the state created by a recorded scenario run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			rec, err := record.Load(args[0])
			if err != nil {
				return fmt.Errorf("loading record: %w", err)
			}

			// Decode record state into cty values once, before we do
			// anything with providers. A malformed record should
			// surface before we spawn subprocesses or open
			// connections.
			vars, err := record.DecodeCty(rec.Variables)
			if err != nil {
				return fmt.Errorf("decoding record variables: %w", err)
			}
			outputs, err := record.IndexOutputs(rec)
			if err != nil {
				return fmt.Errorf("decoding record outputs: %w", err)
			}

			// Reload the scenario. The record's scenario.path is
			// absolute, so it resolves regardless of cwd. Use the
			// recorded scenario name so files with multiple
			// scenarios still pick the right one.
			ps, err := parser.LoadScenario(rec.Scenario.Path, rec.Scenario.Name, nil)
			if err != nil {
				return fmt.Errorf("loading scenario %s: %w", rec.Scenario.Path, err)
			}

			// Sanity check: the record's scenario name should match
			// the scenario the file still declares. Catches the case
			// where the scenario file has been rewritten between run
			// and teardown.
			if ps.Scenario.Name != rec.Scenario.Name {
				return fmt.Errorf(
					"scenario name mismatch: record has %q, file %s now declares %q",
					rec.Scenario.Name, rec.Scenario.Path, ps.Scenario.Name)
			}

			g, err := graph.Build(ps)
			if err != nil {
				return fmt.Errorf("building graph: %w", err)
			}

			reg, err := registerProviders(ps, providerModeRun)
			if err != nil {
				return fmt.Errorf("registering providers: %w", err)
			}
			defer func() {
				if closeErr := reg.Close(); closeErr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", closeErr)
				}
			}()

			// Teardown uses the scenario's provider configs verbatim.
			// The record doesn't capture those today — tricky because
			// they may reference variables whose defaults have
			// changed. For v1, re-evaluate them from the scenario;
			// if provider credentials have moved since the original
			// run, the user fixes them in the scenario (or passes
			// them via --var when we add that).
			if err := reg.Configure(ctx, ps.ProviderConfigs); err != nil {
				return fmt.Errorf("configuring providers: %w", err)
			}

			eng, err := engine.New(reg)
			if err != nil {
				return fmt.Errorf("building engine: %w", err)
			}

			eng.SetReporter(reporter.Select(reporter.Options{
				Out:        cmd.OutOrStdout(),
				ForcePlain: noColorFlag,
			}))

			return eng.Teardown(ctx, ps, g, vars, outputs)
		},
	}
}
