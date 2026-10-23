package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	"github.com/zclconf/go-cty/cty"
)

// TestMySQL_RunAndTeardown runs a scenario against a real MySQL:
// inserts wired together by last_insert_id, a SELECT whose DECIMAL and
// DATETIME columns must decode as a number and an RFC 3339 string, a
// multi-statement transaction, then teardown from the run record. The
// product's foreign key means teardown only succeeds in reverse order.
//
// The container is started here rather than in TestMain so the
// postgres tests don't pay for it.
func TestMySQL_RunAndTeardown(t *testing.T) {
	ctx := context.Background()
	ctr, err := tcmysql.Run(ctx, "mysql:8.4",
		tcmysql.WithDatabase("orchard_test"),
		tcmysql.WithUsername("orchard"),
		tcmysql.WithPassword("orchard"),
	)
	require.NoError(t, err, "starting mysql container")
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(ctr) })

	host, err := ctr.Host(ctx)
	require.NoError(t, err)
	port, err := ctr.MappedPort(ctx, "3306/tcp")
	require.NoError(t, err)

	// The scenario gets a mysql:// URL, so the provider's URL
	// conversion is exercised too; the test's own handle uses the
	// driver format.
	scenarioDSN := fmt.Sprintf("mysql://orchard:orchard@%s:%s/orchard_test", host, port.Port())
	db, err := sql.Open("mysql", fmt.Sprintf("orchard:orchard@tcp(%s:%s)/orchard_test?multiStatements=true", host, port.Port()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.ExecContext(ctx, `
		CREATE TABLE merchants (
			id         INT AUTO_INCREMENT PRIMARY KEY,
			name       VARCHAR(100) NOT NULL,
			region     VARCHAR(10)  NOT NULL,
			created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB;
		CREATE TABLE products (
			id          INT AUTO_INCREMENT PRIMARY KEY,
			merchant_id INT NOT NULL,
			name        VARCHAR(100)  NOT NULL,
			price       DECIMAL(10,2) NOT NULL,
			FOREIGN KEY (merchant_id) REFERENCES merchants (id)
		) ENGINE=InnoDB;`)
	require.NoError(t, err, "creating tables")

	const scenario = "testdata/scenarios/mysql.hcl"
	vars := map[string]string{"dsn": scenarioDSN}
	out, rec := runScenarioWithRecord(t, scenario, vars, t.TempDir())

	merchantID := ctyInt(t, out["merchant_id"])
	productID := ctyInt(t, out["product_id"])
	assert.Positive(t, merchantID)
	assert.Positive(t, productID)
	assert.Equal(t, merchantID, ctyInt(t, out["product_merchant_id"]), "last_insert_id was wired into the product")

	require.Equal(t, cty.Number, out["price"].Type(), "DECIMAL should decode as a number")
	assert.True(t, out["price"].RawEquals(cty.MustParseNumberVal("9.99")), "price: %#v", out["price"])
	_, err = time.Parse(time.RFC3339, ctyString(t, out["created_at"]))
	assert.NoError(t, err, "DATETIME should be an RFC 3339 string")

	var region string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT region FROM merchants WHERE id = ?", merchantID).Scan(&region))
	assert.Equal(t, "eu", region, "the multi-statement transaction should have committed")

	runTeardown(t, scenario, vars, rec)

	for _, table := range []string{"products", "merchants"} {
		var n int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n))
		assert.Zero(t, n, "%s should be empty after teardown", table)
	}
}
