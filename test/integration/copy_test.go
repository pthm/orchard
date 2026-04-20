package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScenario_CopyInlineAndFile exercises the postgres COPY action
// with both data sources:
//
//  1. Inline CSV (two merchants, no header, trimmed heredoc)
//  2. File CSV (three products with header row, file path resolved
//     relative to the scenario file by the engine)
//
// Verifies that:
//   - Both actions produce a rows_copied output with the right count.
//   - The rows actually exist in the database with correct values.
//   - The file-path resolution works — the scenario references
//     "../fixtures/products.csv" and the test runs from
//     test/integration/, not testdata/scenarios/.
func TestScenario_CopyInlineAndFile(t *testing.T) {
	resetDB(t)

	// The inline copy inserts merchants without explicit IDs; the
	// file copy references merchant_id = 1 so we need the inline
	// copy to run first. Orchard resolves this via the dependency
	// graph — there's no explicit depends_on because the actions
	// are independent (products.merchant_id = 1 is a literal, not a
	// reference). For the FK to succeed, seed_merchants must have
	// produced merchant id=1. Since SERIAL restarts at 1 after
	// resetDB's TRUNCATE RESTART IDENTITY, the first copy-inserted
	// merchant gets id=1.
	//
	// But graph order between independent actions is alphabetical by
	// node ID, and "action.copy.seed_merchants" sorts before
	// "action.copy.seed_products", so this works.

	outputs := runScenario(t, "testdata/scenarios/copy.hcl", map[string]string{
		"dsn": testDSN,
	})

	// Check outputs.
	merchantsCopied := ctyInt(t, outputs["merchants_copied"])
	productsCopied := ctyInt(t, outputs["products_copied"])
	assert.Equal(t, int64(2), merchantsCopied, "inline merchants copied")
	assert.Equal(t, int64(3), productsCopied, "file products copied")

	// Verify the inline-loaded merchants actually landed.
	var merchantCount int
	err := testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM merchants").Scan(&merchantCount)
	require.NoError(t, err)
	assert.Equal(t, 2, merchantCount)

	// Spot-check one merchant row.
	var name, region string
	err = testPool.QueryRow(context.Background(),
		"SELECT name, region FROM merchants WHERE name = $1", "Copy Corp").
		Scan(&name, &region)
	require.NoError(t, err)
	assert.Equal(t, "us", region)

	// Verify the file-loaded products.
	var productCount int
	err = testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM products").Scan(&productCount)
	require.NoError(t, err)
	assert.Equal(t, 3, productCount)

	// Spot-check one product row.
	var productName string
	var price float64
	err = testPool.QueryRow(context.Background(),
		"SELECT name, price FROM products WHERE name = $1", "CSV Widget").
		Scan(&productName, &price)
	require.NoError(t, err)
	assert.InDelta(t, 9.99, price, 0.01)
}
