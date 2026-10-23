package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/dbschema"
	"github.com/pthm/orchard/pkg/provider"
	"github.com/pthm/orchard/pkg/snapshot"
)

var (
	schemaScenarioFlag string
	schemaSchemasFlag  []string
	schemaForceFlag    bool
	schemaCheckFlag    bool
)

func newSchemaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Capture the schemas of the systems a scenario acts on",
	}
	cmd.AddCommand(newSchemaPullCmd())
	return cmd
}

func newSchemaPullCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull <scenario>",
		Short: "Snapshot the schema of each database a scenario uses",
		Long: `Connect to each provider the scenario configures, the same way
"orchard run" does, and write a snapshot of every database's tables to
.orchard/schema/<alias>.json. Only the catalog is read; no data is
touched, and no connection details are stored.

The snapshot goes in the nearest .orchard/ directory above the
scenario, or a new one in the current directory. Commit snapshots so
"orchard plan" can check scenarios against them without a database,
and re-run pull after migrations.

pull won't replace a snapshot of a different database (two scenarios
using one alias for different databases) unless --force is given.

With --check, pull writes nothing: it compares each snapshot with the
database, lists any differences, and fails if a snapshot is missing or
out of date. Use it in CI.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			ps, err := parser.LoadScenario(args[0], schemaScenarioFlag, varFlags)
			if err != nil {
				return fmt.Errorf("loading scenario: %w", err)
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
			if err := reg.Configure(ctx, ps.ProviderConfigs); err != nil {
				return fmt.Errorf("configuring providers: %w", err)
			}

			project, found := snapshot.FindProject(args[0])
			if !found {
				if project, err = os.Getwd(); err != nil {
					return err
				}
			}

			pulled, stale := 0, 0
			for _, alias := range reg.Names() {
				p, err := reg.Get(alias)
				if err != nil {
					return err
				}
				src, ok := p.(provider.SchemaSource)
				if !ok {
					continue
				}
				path := snapshot.Path(project, alias)
				if schemaCheckFlag {
					ok, err := checkSnapshot(ctx, out, src, path)
					if err != nil {
						return fmt.Errorf("provider %q: %w", alias, err)
					}
					if !ok {
						stale++
					}
					pulled++
					continue
				}
				capture, err := src.PullSchema(ctx, provider.PullOptions{Schemas: schemaSchemasFlag})
				if err != nil {
					return fmt.Errorf("provider %q: %w", alias, err)
				}
				status, err := writeSnapshot(path, alias, ps.RequiredProviders[alias].Source, capture)
				if err != nil {
					return fmt.Errorf("provider %q: %w", alias, err)
				}
				fmt.Fprintf(out, "  %s %s (%s)\n", status, displayPath(path), capture.Target)
				if emptyDatabase(capture) {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s has no tables; check that provider %q points at the right database\n",
						capture.Target, alias)
				}
				pulled++
			}
			if pulled == 0 {
				return errors.New("no provider in this scenario can capture a schema (postgres, mysql and sqlite can)")
			}
			if stale > 0 {
				return fmt.Errorf("%d snapshot(s) missing or out of date; run \"orchard schema pull %s\" to update", stale, args[0])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&schemaScenarioFlag, "scenario", "",
		"Pick a scenario by name when the file defines multiple")
	cmd.Flags().StringSliceVar(&schemaSchemasFlag, "schemas", nil,
		"Only capture these schemas (Postgres; comma-separated)")
	cmd.Flags().BoolVar(&schemaForceFlag, "force", false,
		"Replace a snapshot even if it's of a different database")
	cmd.Flags().BoolVar(&schemaCheckFlag, "check", false,
		"Compare snapshots with the database without writing; fail if any differ")
	return cmd
}

// writeSnapshot stores capture at path, returning "wrote" or
// "unchanged". It refuses to replace a snapshot of a different target
// unless --force is set, and leaves an identical snapshot untouched so
// re-pulling an unchanged schema doesn't produce a diff.
func writeSnapshot(path, alias, source string, capture *provider.Capture) (string, error) {
	existing, err := snapshot.Read(path)
	switch {
	case err == nil:
		if !schemaForceFlag && (existing.Kind != capture.Kind || existing.Target != capture.Target) {
			return "", fmt.Errorf("%s is a snapshot of %s, not %s; give the provider a different alias in this scenario, or pass --force to replace it",
				displayPath(path), existing.Target, capture.Target)
		}
		if sameJSON(existing.Body, capture.Body) && existing.Target == capture.Target &&
			existing.Provider.Source == source && existing.Provider.Alias == alias {
			return "unchanged", nil
		}
	case errors.Is(err, snapshot.ErrNotFound):
	case !schemaForceFlag:
		return "", fmt.Errorf("%w (pass --force to replace it)", err)
	}

	err = snapshot.Write(path, &snapshot.Snapshot{
		Kind:       capture.Kind,
		Target:     capture.Target,
		Provider:   snapshot.Provider{Source: source, Alias: alias},
		CapturedAt: time.Now().UTC().Truncate(time.Second),
		Body:       capture.Body,
	})
	if err != nil {
		return "", err
	}
	return "wrote", nil
}

// checkSnapshot compares the snapshot at path with the database and
// reports the result to out. It returns false when the snapshot is
// missing or out of date.
func checkSnapshot(ctx context.Context, out io.Writer, src provider.SchemaSource, path string) (bool, error) {
	snap, err := snapshot.Read(path)
	if errors.Is(err, snapshot.ErrNotFound) {
		fmt.Fprintf(out, "  missing %s\n", displayPath(path))
		return false, nil
	}
	if err != nil {
		return false, err
	}
	diffs, err := snapshotDiff(ctx, src, snap)
	if err != nil {
		return false, err
	}
	if len(diffs) == 0 {
		fmt.Fprintf(out, "  up to date %s (%s)\n", displayPath(path), snap.Target)
		return true, nil
	}
	fmt.Fprintf(out, "  out of date %s (%s):\n", displayPath(path), snap.Target)
	writeDiffs(out, diffs, "    ")
	return false, nil
}

// snapshotDiff captures the provider's schema again, with the same
// scope as snap, and lists how the database differs from snap.
func snapshotDiff(ctx context.Context, src provider.SchemaSource, snap *snapshot.Snapshot) ([]string, error) {
	var old dbschema.Schema
	if snap.Kind == dbschema.SnapshotKind {
		if err := json.Unmarshal(snap.Body, &old); err != nil {
			return nil, fmt.Errorf("read %s snapshot: %w", snap.Target, err)
		}
	}
	capture, err := src.PullSchema(ctx, provider.PullOptions{Schemas: old.Namespaces})
	if err != nil {
		return nil, err
	}
	if capture.Target != snap.Target {
		return []string{fmt.Sprintf("snapshot is of %s, but the provider now points at %s", snap.Target, capture.Target)}, nil
	}
	if snap.Kind != dbschema.SnapshotKind || capture.Kind != dbschema.SnapshotKind {
		if sameJSON(snap.Body, capture.Body) {
			return nil, nil
		}
		return []string{"schema changed"}, nil
	}
	var cur dbschema.Schema
	if err := json.Unmarshal(capture.Body, &cur); err != nil {
		return nil, err
	}
	return dbschema.Diff(&old, &cur), nil
}

// writeDiffs prints up to ten differences, then a count of the rest.
func writeDiffs(out io.Writer, diffs []string, indent string) {
	const shown = 10
	for i, d := range diffs {
		if i == shown {
			fmt.Fprintf(out, "%s… and %d more\n", indent, len(diffs)-shown)
			return
		}
		fmt.Fprintf(out, "%s- %s\n", indent, d)
	}
}

// warnSnapshotDrift is run's check: for each provider with a committed
// snapshot, warn when the database has drifted from it, so a stale
// snapshot doesn't quietly weaken `orchard plan`. Problems checking are
// warnings too; they never stop the run.
func warnSnapshotDrift(ctx context.Context, errOut io.Writer, reg *provider.Registry, scenario string) {
	snaps, err := loadSnapshots(scenario, reg.Names())
	if err != nil {
		fmt.Fprintf(errOut, "warning: couldn't read schema snapshots: %v\n", err)
		return
	}
	for _, alias := range reg.Names() {
		snap, ok := snaps[alias]
		if !ok {
			continue
		}
		p, _ := reg.Get(alias)
		src, ok := p.(provider.SchemaSource)
		if !ok {
			continue
		}
		diffs, err := snapshotDiff(ctx, src, snap)
		if err != nil {
			fmt.Fprintf(errOut, "warning: couldn't check the %q schema snapshot against the database: %v\n", alias, err)
			continue
		}
		if len(diffs) == 0 {
			continue
		}
		fmt.Fprintf(errOut, "warning: the schema snapshot for provider %q is out of date with the database:\n", alias)
		writeDiffs(errOut, diffs, "  ")
		fmt.Fprintf(errOut, "  Run \"orchard schema pull %s\" to update it; until then, orchard plan checks against the old schema.\n", scenario)
	}
}

// emptyDatabase reports whether capture is a database snapshot with no
// tables, which usually means the provider connected somewhere other
// than intended (a wrong database name, or a relative SQLite path
// resolved from another directory).
func emptyDatabase(capture *provider.Capture) bool {
	if capture.Kind != dbschema.SnapshotKind {
		return false
	}
	var s dbschema.Schema
	return json.Unmarshal(capture.Body, &s) == nil && len(s.Tables) == 0
}

// sameJSON reports whether two JSON documents are equal ignoring
// whitespace.
func sameJSON(a, b json.RawMessage) bool {
	var ca, cb bytes.Buffer
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return false
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}

// displayPath shows path relative to the working directory when it's
// inside it.
func displayPath(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	if rel, err := filepath.Rel(wd, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}
