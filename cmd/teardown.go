package cmd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/cli/reporter"
	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/internal/record"
	"github.com/pthm/orchard/pkg/provider"
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
			undos, err := record.IndexUndos(rec)
			if err != nil {
				return fmt.Errorf("decoding record undo steps: %w", err)
			}

			// Reload the scenario with the variables the run used, so
			// count / for_each expand to the same instances and
			// provider configs see the same values. The record's
			// scenario.path is absolute, so it resolves regardless of
			// cwd. Use the recorded scenario name so files with
			// multiple scenarios still pick the right one.
			ps, err := parser.LoadScenarioValues(rec.Scenario.Path, rec.Scenario.Name, vars)
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
					rec.Scenario.Name, rec.Scenario.Path, ps.Scenario.Name,
				)
			}

			g, err := graph.Build(ps)
			if err != nil {
				return fmt.Errorf("building graph: %w", err)
			}

			// A recorded node that created something but is gone from
			// the scenario would be skipped silently, leaving its
			// state behind while teardown reports success.
			if missing := missingNodes(g, outputs, undos); len(missing) > 0 {
				return fmt.Errorf("scenario %s no longer declares recorded node(s) %s; restore them to tear down",
					rec.Scenario.Path, strings.Join(missing, ", "))
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

			// Provider configs are re-evaluated from the scenario
			// against the recorded variables. If provider credentials
			// have moved since the original run, the user fixes them
			// in the scenario.
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

			return eng.Teardown(ctx, ps, g, vars, outputs, undos)
		},
	}
}

// missingNodes lists, sorted, the recorded nodes with outputs or an
// undo step that g no longer contains.
func missingNodes(g *graph.Graph, outputs map[string]map[string]cty.Value, undos map[string]provider.Invocation) []string {
	var missing []string
	for id := range outputs {
		if _, ok := g.Nodes[graph.NodeID(id)]; !ok {
			missing = append(missing, id)
		}
	}
	for id := range undos {
		if _, ok := g.Nodes[graph.NodeID(id)]; !ok && outputs[id] == nil {
			missing = append(missing, id)
		}
	}
	slices.Sort(missing)
	return missing
}
