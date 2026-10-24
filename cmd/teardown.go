package cmd

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/cli/reporter"
	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/internal/record"
	"github.com/pthm/orchard/pkg/provider"
)

// newTeardownCmd builds the `orchard teardown <record>` subcommand.
// The record is a file path or a run ID looked up in --record-dir, or
// with --latest a scenario file whose newest run not yet fully torn
// down is picked from --record-dir. It
// reads a record produced by a prior `orchard run`,
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
//
// Progress is written back to the record: each node whose teardown
// succeeded is marked torn_down and skipped by a re-run, and once
// nothing fails the record gets torn_down_at and is refused from then
// on.
func newTeardownCmd() *cobra.Command {
	var (
		recordDir    string
		latest       bool
		scenarioName string
	)
	cmd := &cobra.Command{
		Use:   "teardown <record-file | run-id | scenario-file --latest>",
		Short: "Tear down the state created by a recorded scenario run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			var file record.File
			var err error
			switch {
			case latest:
				file, err = latestRecord(recordDir, args[0], scenarioName, cmd.ErrOrStderr())
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "tearing down run %s (%s)\n", file.Record.ID, file.Path)
			case scenarioName != "":
				return fmt.Errorf("--scenario needs --latest")
			default:
				file, err = record.Resolve(recordDir, args[0], cmd.ErrOrStderr())
				if err != nil {
					return fmt.Errorf("loading record: %w", err)
				}
			}
			rec := file.Record
			if rec.TornDownAt != nil {
				return fmt.Errorf("record %s already torn down at %s", file.Path, rec.TornDownAt.Format(time.RFC3339))
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
			var opts []parser.Option
			if rec.Seed != nil {
				// Teardown bodies that call random functions see the
				// run's streams.
				opts = append(opts, parser.WithSeed(*rec.Seed))
			}
			ps, err := parser.LoadScenarioValues(rec.Scenario.Path, rec.Scenario.Name, vars, opts...)
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

			done := map[string]bool{}
			completed := make([]string, 0, len(rec.Nodes))
			for _, n := range rec.Nodes {
				completed = append(completed, n.ID)
				if n.TornDown {
					done[n.ID] = true
				}
			}

			// A recorded node that created something but is gone from
			// the scenario would be skipped silently, leaving its
			// state behind while teardown reports success. Nodes
			// already torn down have nothing left behind.
			if missing := missingNodes(g, outputs, undos, done); len(missing) > 0 {
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

			torn, tdErr := eng.Teardown(ctx, ps, g, vars, outputs, undos, done, completed)
			markTornDown(rec, torn, tdErr == nil)
			if err := record.Save(file.Path, rec); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: saving teardown progress: %v\n", err)
			}
			return tdErr
		},
	}
	cmd.Flags().StringVar(&recordDir, "record-dir", ".",
		"Directory to look a run ID or --latest run up in")
	cmd.Flags().BoolVar(&latest, "latest", false,
		"Treat the argument as a scenario file and tear down its newest run not yet fully torn down")
	cmd.Flags().StringVar(&scenarioName, "scenario", "",
		"With --latest, the scenario to pick when the file declares several")
	return cmd
}

// latestRecord picks the newest record in dir that isn't fully torn
// down and whose scenario lives in the file at path (and, when name
// is set, is that scenario). Runs of several scenarios in the file
// need name to choose between them.
func latestRecord(dir, path, name string, warn io.Writer) (record.File, error) {
	files, err := record.List(dir, warn)
	if err != nil {
		return record.File{}, err
	}
	var match []record.File
	names := map[string]bool{}
	for _, f := range files {
		if f.Record.TornDownAt != nil || !sameFile(path, f.Record.Scenario.Path) {
			continue
		}
		if name != "" && f.Record.Scenario.Name != name {
			continue
		}
		match = append(match, f)
		names[f.Record.Scenario.Name] = true
	}
	if len(match) == 0 {
		return record.File{}, fmt.Errorf("no runs left to tear down for %s in %s", path, dir)
	}
	if len(names) > 1 {
		sorted := slices.Sorted(maps.Keys(names))
		return record.File{}, fmt.Errorf("%s has runs of scenarios %s; pick one with --scenario",
			path, strings.Join(sorted, ", "))
	}
	return match[0], nil
}

// sameFile reports whether a and b name the same file. Stat sees
// through symlinks (macOS's /var is /private/var); when either can't
// be stat'd, their absolute paths are compared instead.
func sameFile(a, b string) bool {
	sa, errA := os.Stat(a)
	sb, errB := os.Stat(b)
	if errA == nil && errB == nil {
		return os.SameFile(sa, sb)
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	return errA == nil && errB == nil && absA == absB
}

// markTornDown flags the torn nodes on rec, and stamps the record as
// fully torn down when complete (no teardown failed).
func markTornDown(rec *record.Record, torn []string, complete bool) {
	for _, id := range torn {
		for i := range rec.Nodes {
			if rec.Nodes[i].ID == id {
				rec.Nodes[i].TornDown = true
			}
		}
	}
	if complete {
		now := time.Now().UTC()
		rec.TornDownAt = &now
	}
}

// missingNodes lists, sorted, the recorded nodes with outputs or an
// undo step that g no longer contains, leaving out those already done.
func missingNodes(g *graph.Graph, outputs map[string]map[string]cty.Value, undos map[string]provider.Invocation, done map[string]bool) []string {
	var missing []string
	for id := range outputs {
		if _, ok := g.Nodes[graph.NodeID(id)]; !ok && !done[id] {
			missing = append(missing, id)
		}
	}
	for id := range undos {
		if _, ok := g.Nodes[graph.NodeID(id)]; !ok && outputs[id] == nil && !done[id] {
			missing = append(missing, id)
		}
	}
	slices.Sort(missing)
	return missing
}
