package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIsolation_SchemaPerRun: the run builds its tables in a schema of
// its own, and one teardown drops the schema with every row in it.
// Two runs get separate schemas, so tearing one down leaves the other.
func TestIsolation_SchemaPerRun(t *testing.T) {
	scenario := "testdata/scenarios/isolation_schema.hcl"
	vars := map[string]string{"dsn": testDSN}
	outA, recA := runScenarioWithRecord(t, scenario, vars, t.TempDir())
	outB, recB := runScenarioWithRecord(t, scenario, vars, t.TempDir())
	a, b := ctyString(t, outA["schema"]), ctyString(t, outB["schema"])
	require.NotEqual(t, a, b, "each run should get its own schema")
	require.Equal(t, 51, countIn(t, "SELECT count(*) FROM "+a+".merchants"))

	runTeardown(t, scenario, recA)
	require.False(t, schemaExists(t, a), "teardown should drop %s", a)
	require.Equal(t, 51, countIn(t, "SELECT count(*) FROM "+b+".merchants"), "the other run's schema stays")

	runTeardown(t, scenario, recB)
	require.False(t, schemaExists(t, b))
}

// TestIsolation_RunTag: rows carry the run's tag, and a single
// teardown deletes by it; products go with their merchants. Rows
// another run tagged, or that nobody tagged, stay.
func TestIsolation_RunTag(t *testing.T) {
	resetDB(t)
	_, err := testPool.Exec(context.Background(), "INSERT INTO merchants (name) VALUES ('untagged')")
	require.NoError(t, err)

	scenario := "testdata/scenarios/isolation_tag.hcl"
	vars := map[string]string{"dsn": testDSN}
	outA, recA := runScenarioWithRecord(t, scenario, vars, t.TempDir())
	_, recB := runScenarioWithRecord(t, scenario, vars, t.TempDir())
	require.Equal(t, 41, countIn(t, "SELECT count(*) FROM merchants"))
	require.Equal(t, 40, countIn(t, "SELECT count(*) FROM products"))

	runTeardown(t, scenario, recA)
	tagA := ctyString(t, outA["run_tag"])
	require.Zero(t, countIn(t, "SELECT count(*) FROM merchants WHERE name LIKE '%#"+tagA+"'"))
	require.Equal(t, 21, countIn(t, "SELECT count(*) FROM merchants"), "run B's rows and the untagged row stay")
	require.Equal(t, 20, countIn(t, "SELECT count(*) FROM products"))

	runTeardown(t, scenario, recB)
	require.Equal(t, 1, countIn(t, "SELECT count(*) FROM merchants"))
	require.Zero(t, countIn(t, "SELECT count(*) FROM products"))
}

func countIn(t *testing.T, query string) int {
	t.Helper()
	var n int
	require.NoError(t, testPool.QueryRow(context.Background(), query).Scan(&n))
	return n
}

func schemaExists(t *testing.T, name string) bool {
	t.Helper()
	return countIn(t, "SELECT count(*) FROM information_schema.schemata WHERE schema_name = '"+name+"'") == 1
}
