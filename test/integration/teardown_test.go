package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/internal/record"
)

// TestTeardown_EndToEnd exercises the full records + teardown cycle.
//
//  1. Run a scenario that inserts a merchant and a product (FK to
//     the merchant) with lifecycle.teardown declared on both.
//  2. Write the run record to a temp dir via the RecordingReporter
//     (same path the CLI uses in `orchard run`).
//  3. Query the DB directly to confirm both rows exist.
//  4. Load the record from disk.
//  5. Invoke engine.Teardown with the decoded record state.
//  6. Query again to confirm both rows are gone.
//
// The FK between products.merchant_id and merchants.id is the
// teeth of the test: teardown must walk reverse-topologically
// (product before merchant), or the first delete fails with an FK
// constraint violation. That confirms the graph-traversal direction
// the engine promises.
func TestTeardown_EndToEnd(t *testing.T) {
	resetDB(t)

	scenarioPath := "testdata/scenarios/teardown.hcl"
	recordDir := t.TempDir()

	// --- Run the scenario with recording enabled ---
	runOutputs, rec := runScenarioWithRecord(t, scenarioPath, map[string]string{
		"dsn": testDSN,
	}, recordDir)

	merchantID := ctyInt(t, runOutputs["merchant_id"])
	productID := ctyInt(t, runOutputs["product_id"])
	require.Positive(t, merchantID, "expected a merchant id from the run")
	require.Positive(t, productID, "expected a product id from the run")

	// --- DB should reflect the created state ---
	assertMerchantExists(t, merchantID, true)
	assertProductExists(t, productID, true)

	// --- Run teardown against the recorded state ---
	runTeardown(t, scenarioPath, map[string]string{"dsn": testDSN}, rec)

	// --- DB should reflect the torn-down state ---
	assertMerchantExists(t, merchantID, false)
	assertProductExists(t, productID, false)
}

// runScenarioWithRecord executes a scenario with a RecordingReporter
// wrapping a silent inner reporter, returns the scenario outputs,
// and returns the persisted record loaded from disk. The record file
// lives in recordDir so a single test can locate it without
// searching cwd.
func runScenarioWithRecord(
	t *testing.T, path string, vars map[string]string, recordDir string,
) (outputs map[string]cty.Value, rec *record.Record) {
	t.Helper()

	ps, err := parser.LoadScenario(path, "", vars)
	require.NoError(t, err)

	g, err := graph.Build(ps)
	require.NoError(t, err)

	reg, err := buildRegistry(ps)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reg.Close() })

	eng, err := engine.New(reg)
	require.NoError(t, err)

	require.NoError(t, eng.ValidateGraph(g))
	require.NoError(t, eng.ValidateReferences(context.Background(), ps, g))

	rep, err := record.NewReporter(record.Options{
		Writer:    record.FileWriter{Dir: recordDir},
		Scenario:  record.ScenarioFromParser(ps, path),
		Variables: ps.Variables,
		LogOutput: os.Stderr,
	})
	require.NoError(t, err)
	eng.SetReporter(rep)

	result, err := eng.Execute(context.Background(), ps, g)
	require.NoError(t, err, "run failed")
	require.NoError(t, rep.WriteErr, "record write failed")

	// Locate the one record file the run produced.
	entries, err := os.ReadDir(recordDir)
	require.NoError(t, err)
	var recordPath string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			recordPath = filepath.Join(recordDir, e.Name())
			break
		}
	}
	require.NotEmpty(t, recordPath, "no record file written")

	loaded, err := record.Load(recordPath)
	require.NoError(t, err)
	require.Equal(t, record.StatusSucceeded, loaded.Status)

	// Convert outputs to the local typed alias so the caller can use
	// the existing ctyInt/ctyString helpers without a second import.
	out := make(map[string]cty.Value, len(result.Outputs))
	for k, v := range result.Outputs {
		out[k] = v
	}
	return out, loaded
}

// runTeardown replays the engine.Teardown pass against a record the
// test already produced. Mirrors what `orchard teardown` does but
// calls the engine directly so the integration harness doesn't have
// to shell out to the binary.
func runTeardown(t *testing.T, scenarioPath string, vars map[string]string, rec *record.Record) {
	t.Helper()

	ps, err := parser.LoadScenario(scenarioPath, "", vars)
	require.NoError(t, err)

	g, err := graph.Build(ps)
	require.NoError(t, err)

	reg, err := buildRegistry(ps)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reg.Close() })

	eng, err := engine.New(reg)
	require.NoError(t, err)

	recVars, err := record.DecodeCty(rec.Variables)
	require.NoError(t, err)
	outputs, err := record.IndexOutputs(rec)
	require.NoError(t, err)

	require.NoError(t, eng.Teardown(context.Background(), ps, g, recVars, outputs),
		"teardown reported errors")
}

// assertMerchantExists checks whether a merchant row with the given
// id is still in the database. Wraps the SQL round-trip in a single
// assertion so the failure message reads cleanly.
func assertMerchantExists(t *testing.T, id int64, want bool) {
	t.Helper()
	var count int
	err := testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM merchants WHERE id = $1", id).Scan(&count)
	require.NoError(t, err)
	if want {
		assert.Equal(t, 1, count, "merchant %d should exist", id)
	} else {
		assert.Equal(t, 0, count, "merchant %d should be gone", id)
	}
}

func assertProductExists(t *testing.T, id int64, want bool) {
	t.Helper()
	var count int
	err := testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM products WHERE id = $1", id).Scan(&count)
	require.NoError(t, err)
	if want {
		assert.Equal(t, 1, count, "product %d should exist", id)
	} else {
		assert.Equal(t, 0, count, "product %d should be gone", id)
	}
}
