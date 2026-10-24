package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/provider/postgres"
)

// TestPostgresQuery_Transaction checks that a postgres_query wrapping
// several statements in BEGIN; ... COMMIT; is atomic: both rows land,
// or, when a later statement fails, neither does.
func TestPostgresQuery_Transaction(t *testing.T) {
	ctx := context.Background()
	p := postgres.New()
	require.NoError(t, p.Configure(ctx, map[string]cty.Value{"dsn": cty.StringVal(testDSN)}))
	t.Cleanup(func() { _ = p.Close() })

	query := func(q string) (map[string]cty.Value, error) {
		return p.Execute(ctx, "postgres_query", map[string]cty.Value{"query": cty.StringVal(q)})
	}
	count := func() int {
		var n int
		require.NoError(t, testPool.QueryRow(ctx, "SELECT COUNT(*) FROM merchants").Scan(&n))
		return n
	}

	resetDB(t)
	_, err := query("BEGIN; INSERT INTO merchants (name) VALUES ('a'); INSERT INTO merchants (name) VALUES ('b'); COMMIT;")
	require.NoError(t, err)
	assert.Equal(t, 2, count())

	resetDB(t)
	_, err = query("BEGIN; INSERT INTO merchants (name) VALUES ('a'); INSERT INTO merchants (name) VALUES (NULL); COMMIT;")
	require.Error(t, err)
	assert.Equal(t, 0, count())

	// Without BEGIN/COMMIT the statements still share one implicit
	// transaction, and a failure after a returned row is reported.
	resetDB(t)
	_, err = query("INSERT INTO merchants (name) VALUES ('a') RETURNING id; INSERT INTO merchants (name) VALUES (NULL);")
	require.Error(t, err)
	assert.Equal(t, 0, count())

	// A BEGIN without its COMMIT fails rather than reporting success
	// for writes the pool would roll back.
	resetDB(t)
	_, err = query("BEGIN; INSERT INTO merchants (name) VALUES ('z');")
	require.ErrorContains(t, err, "transaction open")
	assert.Equal(t, 0, count())

	// timestamptz comes back in UTC whatever the session's time zone.
	la := postgres.New()
	require.NoError(t, la.Configure(ctx, map[string]cty.Value{"dsn": cty.StringVal(testDSN + "&timezone=America/Los_Angeles")}))
	t.Cleanup(func() { _ = la.Close() })
	out, err := la.Execute(ctx, "postgres_query", map[string]cty.Value{
		"query": cty.StringVal("SELECT '2026-10-23 10:30:00+00'::timestamptz AS at"),
	})
	require.NoError(t, err)
	assert.Equal(t, "2026-10-23T10:30:00Z", ctyString(t, out["at"]))

	// Single statements still expose the first returned row.
	resetDB(t)
	out, err = query("INSERT INTO merchants (name) VALUES ('c') RETURNING id, name")
	require.NoError(t, err)
	assert.Equal(t, "c", ctyString(t, out["name"]))
	assert.Equal(t, int64(1), ctyInt(t, out["id"]))
}
