package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pthm/orchard/internal/cli/reporter"
	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/internal/record"

	// Side-effect imports register builtin provider sources. Adding
	// a new builtin provider is a one-line change here.
	_ "github.com/pthm/orchard/pkg/provider/exec"
	_ "github.com/pthm/orchard/pkg/provider/grpc"
	_ "github.com/pthm/orchard/pkg/provider/http"
	_ "github.com/pthm/orchard/pkg/provider/mysql"
	_ "github.com/pthm/orchard/pkg/provider/postgres"
	_ "github.com/pthm/orchard/pkg/provider/redis"
	_ "github.com/pthm/orchard/pkg/provider/s3"
	_ "github.com/pthm/orchard/pkg/provider/sqlite"
)

// Flags scoped to `orchard run`. Kept package-level so cobra's
// argument parser writes into them, matching the pattern the
// existing global flags use.
var (
	recordDirFlag   string
	noRecordFlag    bool
	runScenarioFlag string
)

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <scenario>",
		Short: "Execute a scenario",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			ps, err := parser.LoadScenario(args[0], runScenarioFlag, varFlags)
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
			reg, err := registerProviders(ps, providerModeRun)
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
			warnSnapshotDrift(ctx, cmd.ErrOrStderr(), reg, args[0])

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
			if err := eng.ValidateReferences(ctx, ps, g); err != nil {
				return fmt.Errorf("validation failed: %w", err)
			}

			cliRep := reporter.Select(reporter.Options{
				Out:        cmd.OutOrStdout(),
				ForcePlain: noColorFlag,
			})

			// Wrap the presentation reporter in a recording reporter
			// unless the user opted out. Records persist to the
			// --record-dir (default cwd) after the run finishes,
			// including on failure so partial runs stay inspectable.
			finalReporter := cliRep
			var recRep *record.Reporter
			if !noRecordFlag {
				recRep, err = record.NewReporter(record.Options{
					Inner:     cliRep,
					Writer:    record.FileWriter{Dir: recordDirFlag},
					Scenario:  record.ScenarioFromParser(ps, args[0]),
					Variables: ps.Variables,
					LogOutput: cmd.ErrOrStderr(),
				})
				if err != nil {
					return fmt.Errorf("preparing record: %w", err)
				}
				finalReporter = recRep
			}
			eng.SetReporter(finalReporter)

			_, execErr := eng.Execute(ctx, ps, g)
			if recRep != nil && recRep.Path() != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "record: %s\n", recRep.Path())
			}
			// Propagate Execute's error after letting the record land —
			// the record reporter has already fired from inside Execute.
			return execErr
		},
	}

	cmd.Flags().StringVar(&recordDirFlag, "record-dir", ".",
		"Directory to write the run record to (default cwd)")
	cmd.Flags().BoolVar(&noRecordFlag, "no-record", false,
		"Skip writing a run record")
	cmd.Flags().StringVar(&runScenarioFlag, "scenario", "",
		"Pick a scenario by name when the file defines multiple")

	return cmd
}
