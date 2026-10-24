package integration_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pthm/orchard/internal/record"
)

// TestParallelTypedActions: twenty inserts and their own-row updates
// at parallelism 8 each record an undo naming their own row, and
// teardown leaves the table empty.
func TestParallelTypedActions(t *testing.T) {
	resetDB(t)
	scenario := "testdata/scenarios/parallel_postgres.hcl"
	vars := map[string]string{"dsn": testDSN}
	_, rec := runScenarioParallel(t, scenario, vars, t.TempDir(), 8)

	outputs, err := record.IndexOutputs(rec)
	require.NoError(t, err)
	undos, err := record.IndexUndos(rec)
	require.NoError(t, err)
	require.Len(t, undos, 40)
	for i := range 20 {
		id := outputs[fmt.Sprintf("action.postgres_insert.m[%d]", i)]["id"]
		del := undos[fmt.Sprintf("action.postgres_insert.m[%d]", i)]
		assert.Equal(t, "postgres_delete", del.Action)
		assert.True(t, del.Attrs["where"].GetAttr("id").Equals(id).True(), "insert %d undo deletes its own row", i)

		restore := undos[fmt.Sprintf("action.postgres_update.rename[%d]", i)]
		assert.Equal(t, "postgres_restore", restore.Action)
		rows := restore.Attrs["rows"].AsValueSlice()
		require.Len(t, rows, 1)
		assert.True(t, rows[0].GetAttr("id").Equals(id).True(), "update %d undo restores its own row", i)
		assert.Equal(t, fmt.Sprintf("m%d", i), rows[0].GetAttr("name").AsString())
	}

	var n int
	require.NoError(t, testPool.QueryRow(context.Background(), "SELECT COUNT(*) FROM merchants WHERE name LIKE '% Ltd'").Scan(&n))
	assert.Equal(t, 20, n)

	runTeardown(t, scenario, rec)
	require.NoError(t, testPool.QueryRow(context.Background(), "SELECT COUNT(*) FROM merchants").Scan(&n))
	assert.Equal(t, 0, n, "teardown should leave merchants empty")
}
