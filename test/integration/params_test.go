package integration_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/provider"
	"github.com/pthm/orchard/pkg/provider/mysql"
	"github.com/pthm/orchard/pkg/provider/postgres"
)

// awkward is a value that breaks string-built SQL.
const awkward = `O'Brien"); DROP TABLE params_test; --`

func query(t *testing.T, p provider.Provider, action, q string, params ...cty.Value) (map[string]cty.Value, error) {
	t.Helper()
	attrs := map[string]cty.Value{"query": cty.StringVal(q)}
	if params != nil {
		attrs["params"] = cty.TupleVal(params)
	}
	return p.Execute(context.Background(), action, attrs)
}

func TestPostgresQuery_Params(t *testing.T) {
	ctx := context.Background()
	p := postgres.New()
	require.NoError(t, p.Configure(ctx, map[string]cty.Value{"dsn": cty.StringVal(testDSN)}))
	t.Cleanup(func() { _ = p.Close() })
	_, err := testPool.Exec(ctx, `DROP TABLE IF EXISTS params_test;
		CREATE TABLE params_test (id serial PRIMARY KEY, name text NOT NULL, note text, n int, price numeric, tags text[], meta jsonb, ok bool)`)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = testPool.Exec(ctx, "DROP TABLE params_test") })

	out, err := query(t, p, "postgres_query",
		"INSERT INTO params_test (name, note, n, price, tags, meta, ok) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *",
		cty.StringVal(awkward), cty.NullVal(cty.String), cty.NumberIntVal(42), cty.MustParseNumberVal("19.99"),
		cty.TupleVal([]cty.Value{cty.StringVal("a"), cty.StringVal("b")}),
		cty.ObjectVal(map[string]cty.Value{"plan": cty.StringVal("pro")}), cty.True)
	require.NoError(t, err)
	assert.Equal(t, awkward, out["name"].AsString(), "the value is stored as written, not run as SQL")
	assert.True(t, out["note"].IsNull())
	assert.True(t, out["n"].RawEquals(cty.NumberIntVal(42)))
	assert.True(t, out["price"].RawEquals(cty.MustParseNumberVal("19.99")), "%#v", out["price"])
	assert.True(t, out["ok"].True())

	var meta string
	require.NoError(t, testPool.QueryRow(ctx, "SELECT meta->>'plan' FROM params_test").Scan(&meta))
	assert.Equal(t, "pro", meta, "an object binds as JSON")

	// A list binds as an array.
	out, err = query(t, p, "postgres_query", "SELECT count(*) AS n FROM params_test WHERE name = ANY($1) AND 'b' = ANY(tags)",
		cty.TupleVal([]cty.Value{cty.StringVal("x"), cty.StringVal(awkward)}))
	require.NoError(t, err)
	assert.True(t, out["n"].RawEquals(cty.NumberIntVal(1)), "%#v", out["n"])

	// Types sqlrow alone would print as Go values.
	out, err = query(t, p, "postgres_query",
		"SELECT tags, meta, '6f1c1a54-3b0e-4d2b-9a57-2f1f4a0b9e01'::uuid AS u, interval '1 day' AS iv, $1::numeric AS big FROM params_test",
		cty.MustParseNumberVal("12345678901234567890.5"))
	require.NoError(t, err)
	assert.True(t, out["tags"].RawEquals(cty.TupleVal([]cty.Value{cty.StringVal("a"), cty.StringVal("b")})), "%#v", out["tags"])
	assert.JSONEq(t, `{"plan":"pro"}`, out["meta"].AsString())
	assert.Equal(t, "6f1c1a54-3b0e-4d2b-9a57-2f1f4a0b9e01", out["u"].AsString())
	assert.Equal(t, "1 day 00:00:00", out["iv"].AsString())
	assert.True(t, out["big"].RawEquals(cty.MustParseNumberVal("12345678901234567890.5")), "%#v", out["big"])

	_, err = query(t, p, "postgres_query", "INSERT INTO params_test (name) VALUES ($1); SELECT 1", cty.StringVal("x"))
	require.ErrorContains(t, err, "a query with params must be a single statement")

	_, err = query(t, p, "postgres_query", "INSERT INTO params_test (name) VALUES ($1)")
	require.Error(t, err, "a placeholder with no params should fail, not insert NULL")

	// Without params, several statements still work.
	_, err = query(t, p, "postgres_query", "SELECT 1; SELECT 2")
	require.NoError(t, err)
}

func TestMySQLQuery_Params(t *testing.T) {
	ctx := context.Background()
	host, port := mysqlServer(t)
	p := mysql.New()
	require.NoError(t, p.Configure(ctx, map[string]cty.Value{"dsn": cty.StringVal(fmt.Sprintf("orchard:orchard@tcp(%s:%s)/orchard_test", host, port))}))
	t.Cleanup(func() { _ = p.Close() })
	_, err := query(t, p, "mysql_query", `DROP TABLE IF EXISTS params_test;
		CREATE TABLE params_test (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(100) NOT NULL, note TEXT, price DECIMAL(10,2), meta JSON)`)
	require.NoError(t, err)

	out, err := query(t, p, "mysql_query", "INSERT INTO params_test (name, note, price, meta) VALUES (?, ?, ?, ?)",
		cty.StringVal(awkward), cty.NullVal(cty.String), cty.MustParseNumberVal("19.99"),
		cty.ObjectVal(map[string]cty.Value{"plan": cty.StringVal("pro")}))
	require.NoError(t, err)
	assert.True(t, out["rows_affected"].RawEquals(cty.NumberIntVal(1)))

	out, err = query(t, p, "mysql_query", "SELECT name, note, price, meta->>'$.plan' AS plan FROM params_test WHERE id = ?", out["last_insert_id"])
	require.NoError(t, err)
	assert.Equal(t, awkward, out["name"].AsString())
	assert.True(t, out["note"].IsNull())
	assert.True(t, out["price"].RawEquals(cty.MustParseNumberVal("19.99")), "%#v", out["price"])
	assert.Equal(t, "pro", out["plan"].AsString())
}
