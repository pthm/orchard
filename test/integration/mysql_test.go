package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
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
// The MySQL container is shared by the MySQL tests and started on
// first use, so the postgres tests don't pay for it.
func TestMySQL_RunAndTeardown(t *testing.T) {
	ctx := context.Background()
	host, port := mysqlServer(t)

	// The scenario gets a mysql:// URL, so the provider's URL
	// conversion is exercised too; the test's own handle uses the
	// driver format.
	scenarioDSN := fmt.Sprintf("mysql://orchard:orchard@%s:%s/orchard_test", host, port)
	db, err := sql.Open("mysql", fmt.Sprintf("orchard:orchard@tcp(%s:%s)/orchard_test?multiStatements=true", host, port))
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

var (
	mysqlOnce            sync.Once
	mysqlHost, mysqlPort string
	mysqlErr             error
)

// mysqlServer starts a MySQL 8.4 container on first use and returns
// its host and port. The user orchard (password orchard) owns the
// orchard_test database; root's password is also orchard. The
// container is terminated after every test has run.
func mysqlServer(t *testing.T) (host, port string) {
	t.Helper()
	mysqlOnce.Do(func() {
		ctx := context.Background()
		ctr, err := tcmysql.Run(ctx, "mysql:8.4",
			tcmysql.WithDatabase("orchard_test"),
			tcmysql.WithUsername("orchard"),
			tcmysql.WithPassword("orchard"),
		)
		if err != nil {
			mysqlErr = fmt.Errorf("starting mysql container: %w", err)
			return
		}
		afterAll = append(afterAll, func() { _ = testcontainers.TerminateContainer(ctr) })
		if mysqlHost, mysqlErr = ctr.Host(ctx); mysqlErr != nil {
			return
		}
		mapped, err := ctr.MappedPort(ctx, "3306/tcp")
		if err != nil {
			mysqlErr = err
			return
		}
		mysqlPort = mapped.Port()
	})
	require.NoError(t, mysqlErr)
	return mysqlHost, mysqlPort
}
