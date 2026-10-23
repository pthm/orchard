package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/provider"
	"github.com/pthm/orchard/pkg/snapshot"
)

// TestPostgresTypedActions runs insert, update and delete against the
// migrations' tables: the inserted row comes back with its defaults,
// numeric price as a number, and the merchant's id wired into the
// product.
func TestPostgresTypedActions(t *testing.T) {
	resetDB(t)
	out := runScenario(t, "testdata/scenarios/typed_postgres.hcl", map[string]string{"dsn": testDSN})

	merchantID := ctyInt(t, out["merchant_id"])
	assert.Positive(t, merchantID)
	assert.Equal(t, "us", ctyString(t, out["default_region"]), "column defaults come back in the row")
	_, err := time.Parse(time.RFC3339Nano, ctyString(t, out["created_at"]))
	assert.NoError(t, err, "timestamptz should be an RFC 3339 string")
	assert.True(t, out["price"].RawEquals(cty.MustParseNumberVal("9.99")), "price: %#v", out["price"])
	assert.Equal(t, merchantID, ctyInt(t, out["product_owner"]))
	assert.Equal(t, int64(1), ctyInt(t, out["moved"]))
	assert.Equal(t, int64(1), ctyInt(t, out["deleted"]))

	var region string
	require.NoError(t, testPool.QueryRow(context.Background(), "SELECT region FROM merchants WHERE id = $1", merchantID).Scan(&region))
	assert.Equal(t, "eu", region)
}

// TestPostgresTypedValidationBeforeRun: a typed action that doesn't fit
// the live schema fails validation (as `orchard run` does it, after
// Configure), and nothing has been written.
func TestPostgresTypedValidationBeforeRun(t *testing.T) {
	resetDB(t)
	err := validateLive(t, "testdata/scenarios/typed_postgres_invalid.hcl", map[string]string{"dsn": testDSN})
	require.Error(t, err)
	for _, want := range []string{"action.postgres_insert.product", `no column "cost"`, `missing required column "price"`} {
		assert.Contains(t, err.Error(), want)
	}
	var n int
	require.NoError(t, testPool.QueryRow(context.Background(), "SELECT COUNT(*) FROM merchants").Scan(&n))
	assert.Zero(t, n, "no action should have run")
}

// TestMySQLTypedActions covers what's different about MySQL: no
// RETURNING (the row is read back by primary key), tinyint(1) booleans
// and JSON columns decoded by kind.
func TestMySQLTypedActions(t *testing.T) {
	ctx := context.Background()
	host, port := mysqlServer(t)
	root, err := sql.Open("mysql", fmt.Sprintf("root:orchard@tcp(%s:%s)/?multiStatements=true", host, port))
	require.NoError(t, err)
	t.Cleanup(func() { _ = root.Close() })
	_, err = root.ExecContext(ctx, `
		DROP DATABASE IF EXISTS typed_test;
		CREATE DATABASE typed_test;
		USE typed_test;
		CREATE TABLE merchants (
			id       INT AUTO_INCREMENT PRIMARY KEY,
			name     VARCHAR(100) NOT NULL,
			active   TINYINT(1) NOT NULL DEFAULT 1,
			settings JSON
		) ENGINE=InnoDB;
		CREATE TABLE products (
			id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			merchant_id INT NOT NULL,
			name        VARCHAR(100) NOT NULL,
			price       DECIMAL(10,2) NOT NULL,
			FOREIGN KEY (merchant_id) REFERENCES merchants (id)
		) ENGINE=InnoDB;`)
	require.NoError(t, err)

	dsn := fmt.Sprintf("mysql://root:orchard@%s:%s/typed_test", host, port)
	out := runScenario(t, "testdata/scenarios/typed_mysql.hcl", map[string]string{"dsn": dsn})

	merchantID := ctyInt(t, out["merchant_id"])
	assert.Positive(t, merchantID)
	assert.True(t, out["active"].RawEquals(cty.True), "tinyint(1) should decode as a bool: %#v", out["active"])
	assert.Equal(t, "gold", ctyString(t, out["tier"]), "JSON columns should decode to values")
	assert.True(t, out["price"].RawEquals(cty.MustParseNumberVal("9.99")), "price: %#v", out["price"])
	assert.Equal(t, merchantID, ctyInt(t, out["product_owner"]))
	assert.Equal(t, int64(1), ctyInt(t, out["deactivated"]))
	assert.Equal(t, int64(1), ctyInt(t, out["deleted"]))

	var active bool
	require.NoError(t, root.QueryRowContext(ctx, "SELECT active FROM typed_test.merchants WHERE id = ?", merchantID).Scan(&active))
	assert.False(t, active)
}

// validateLive runs the pre-execution validation `orchard run` does:
// after Configure, so typed actions are checked against the live
// database.
func validateLive(t *testing.T, path string, vars map[string]string) error {
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
	return eng.ValidateReferences(context.Background(), ps, g)
}

// TestPlanFromSnapshots plans the typed scenarios the way `orchard
// plan` does — providers never configured, only given a snapshot taken
// from the live database — for Postgres and MySQL.
func TestPlanFromSnapshots(t *testing.T) {
	ctx := context.Background()
	pgSnap := captureSnapshot(t, "builtin/postgres", "postgres", map[string]cty.Value{"dsn": cty.StringVal(testDSN)})

	require.NoError(t, planOffline(t, "testdata/scenarios/typed_postgres.hcl", "postgres", pgSnap),
		"the valid postgres scenario should plan")
	err := planOffline(t, "testdata/scenarios/typed_postgres_invalid.hcl", "postgres", pgSnap)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `missing required column "price"`)

	// A postgres snapshot can't stand in for a mysql database.
	err = planOffline(t, "testdata/scenarios/typed_mysql.hcl", "mysql", pgSnap)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is of a postgres database, not mysql")

	host, port := mysqlServer(t)
	root, err := sql.Open("mysql", fmt.Sprintf("root:orchard@tcp(%s:%s)/?multiStatements=true", host, port))
	require.NoError(t, err)
	t.Cleanup(func() { _ = root.Close() })
	_, err = root.ExecContext(ctx, `
		CREATE DATABASE IF NOT EXISTS plan_test;
		USE plan_test;
		CREATE TABLE IF NOT EXISTS merchants (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(100) NOT NULL, active TINYINT(1) NOT NULL DEFAULT 1, settings JSON);
		CREATE TABLE IF NOT EXISTS products (id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, merchant_id INT NOT NULL, name VARCHAR(100) NOT NULL, price DECIMAL(10,2) NOT NULL);`)
	require.NoError(t, err)
	mySnap := captureSnapshot(t, "builtin/mysql", "mysql", map[string]cty.Value{
		"dsn": cty.StringVal(fmt.Sprintf("mysql://root:orchard@%s:%s/plan_test", host, port)),
	})
	require.NoError(t, planOffline(t, "testdata/scenarios/typed_mysql.hcl", "mysql", mySnap),
		"the valid mysql scenario should plan")
}

// captureSnapshot configures a provider against a live database and
// captures its schema as `orchard schema pull` would.
func captureSnapshot(t *testing.T, source, alias string, config map[string]cty.Value) *snapshot.Snapshot {
	t.Helper()
	p, err := provider.NewBuiltin(source)
	require.NoError(t, err)
	require.NoError(t, p.Configure(context.Background(), config))
	t.Cleanup(func() { _ = p.Close() })
	capture, err := p.(provider.SchemaSource).PullSchema(context.Background(), provider.PullOptions{})
	require.NoError(t, err)
	return &snapshot.Snapshot{
		Format: snapshot.Format, Kind: capture.Kind, Target: capture.Target,
		Provider: snapshot.Provider{Source: source, Alias: alias}, Body: capture.Body,
	}
}

// planOffline validates a scenario the way `orchard plan` does:
// providers are registered and given the snapshot, never configured.
func planOffline(t *testing.T, path, alias string, snap *snapshot.Snapshot) error {
	t.Helper()
	ps, err := parser.LoadScenario(path, "", map[string]string{"dsn": "unused"})
	require.NoError(t, err)
	g, err := graph.Build(ps)
	require.NoError(t, err)
	reg := provider.NewRegistry()
	for name, spec := range ps.RequiredProviders {
		p, err := provider.NewBuiltin(spec.Source)
		require.NoError(t, err)
		require.NoError(t, reg.Register(name, p))
	}
	if err := reg.PlanConfigure(context.Background(), ps.ProviderConfigs, map[string]*snapshot.Snapshot{alias: snap}); err != nil {
		return err
	}
	eng, err := engine.New(reg)
	require.NoError(t, err)
	require.NoError(t, eng.ValidateGraph(g))
	return eng.ValidateReferences(context.Background(), ps, g)
}

// TestManagedTeardown runs chained typed inserts with no lifecycle
// blocks and tears them down from the record, on Postgres and MySQL.
// The products foreign key means the undo steps must run in reverse.
func TestManagedTeardown(t *testing.T) {
	ctx := context.Background()

	t.Run("postgres", func(t *testing.T) {
		resetDB(t)
		scenario := "testdata/scenarios/typed_teardown_postgres.hcl"
		vars := map[string]string{"dsn": testDSN}
		out, rec := runScenarioWithRecord(t, scenario, vars, t.TempDir())
		assertMerchantExists(t, ctyInt(t, out["merchant_id"]), true)
		assertProductExists(t, ctyInt(t, out["product_id"]), true)

		runTeardown(t, scenario, vars, rec)
		assertMerchantExists(t, ctyInt(t, out["merchant_id"]), false)
		assertProductExists(t, ctyInt(t, out["product_id"]), false)
	})

	t.Run("mysql", func(t *testing.T) {
		host, port := mysqlServer(t)
		root, err := sql.Open("mysql", fmt.Sprintf("root:orchard@tcp(%s:%s)/?multiStatements=true", host, port))
		require.NoError(t, err)
		t.Cleanup(func() { _ = root.Close() })
		_, err = root.ExecContext(ctx, `
			DROP DATABASE IF EXISTS teardown_test;
			CREATE DATABASE teardown_test;
			USE teardown_test;
			CREATE TABLE merchants (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(100) NOT NULL) ENGINE=InnoDB;
			CREATE TABLE products (
				id INT AUTO_INCREMENT PRIMARY KEY, merchant_id INT NOT NULL, name VARCHAR(100) NOT NULL, price DECIMAL(10,2) NOT NULL,
				FOREIGN KEY (merchant_id) REFERENCES merchants (id)
			) ENGINE=InnoDB;`)
		require.NoError(t, err)

		scenario := "testdata/scenarios/typed_teardown_mysql.hcl"
		vars := map[string]string{"dsn": fmt.Sprintf("mysql://root:orchard@%s:%s/teardown_test", host, port)}
		_, rec := runScenarioWithRecord(t, scenario, vars, t.TempDir())
		runTeardown(t, scenario, vars, rec)

		for _, table := range []string{"products", "merchants"} {
			var n int
			require.NoError(t, root.QueryRowContext(ctx, "SELECT COUNT(*) FROM teardown_test."+table).Scan(&n))
			assert.Zero(t, n, "%s should be empty after teardown", table)
		}
	})
}
