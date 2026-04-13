package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/internal/provider"
	sqlprovider "github.com/pthm/orchard/internal/provider/sql"
)

// runScenario loads, plans, and executes an HCL scenario against the shared
// test Postgres pool and returns the resulting scenario outputs.
func runScenario(t *testing.T, path string, vars map[string]string) map[string]cty.Value {
	t.Helper()

	ps, err := parser.LoadScenario(path, vars)
	require.NoError(t, err, "loading scenario")

	g, err := graph.Build(ps)
	require.NoError(t, err, "building graph")

	reg := provider.NewRegistry()
	reg.Register("sql", sqlprovider.NewProvider(testPool))

	eng := engine.New(reg)
	result, err := eng.Execute(context.Background(), ps, g)
	require.NoError(t, err, "executing scenario")

	return result.Outputs
}

func ctyString(t *testing.T, v cty.Value) string {
	t.Helper()
	require.Equal(t, cty.String, v.Type())
	return v.AsString()
}

func ctyInt(t *testing.T, v cty.Value) int64 {
	t.Helper()
	require.Equal(t, cty.Number, v.Type())
	n, _ := v.AsBigFloat().Int64()
	return n
}

func TestScenario_Basic(t *testing.T) {
	resetDB(t)

	outputs := runScenario(t, "testdata/scenarios/basic.hcl", nil)

	merchantID := ctyInt(t, outputs["merchant_id"])
	productID := ctyInt(t, outputs["product_id"])

	assert.Equal(t, "Acme Corp", ctyString(t, outputs["merchant_name"]))
	assert.Positive(t, merchantID)
	assert.Positive(t, productID)

	var (
		dbName   string
		dbRegion string
	)
	err := testPool.QueryRow(context.Background(),
		"SELECT name, region FROM merchants WHERE id = $1", merchantID).
		Scan(&dbName, &dbRegion)
	require.NoError(t, err)
	assert.Equal(t, "Acme Corp", dbName)
	assert.Equal(t, "us", dbRegion)

	var (
		productMerchantID int64
		productName       string
	)
	err = testPool.QueryRow(context.Background(),
		"SELECT merchant_id, name FROM products WHERE id = $1", productID).
		Scan(&productMerchantID, &productName)
	require.NoError(t, err)
	assert.Equal(t, merchantID, productMerchantID)
	assert.Equal(t, "Widget", productName)
}

func TestScenario_Basic_WithVarOverride(t *testing.T) {
	resetDB(t)

	outputs := runScenario(t, "testdata/scenarios/basic.hcl", map[string]string{
		"merchant_name": "Override Inc",
		"region":        "eu",
	})

	assert.Equal(t, "Override Inc", ctyString(t, outputs["merchant_name"]))

	var region string
	err := testPool.QueryRow(context.Background(),
		"SELECT region FROM merchants WHERE id = $1",
		ctyInt(t, outputs["merchant_id"])).
		Scan(&region)
	require.NoError(t, err)
	assert.Equal(t, "eu", region)
}

func TestScenario_WithComponent(t *testing.T) {
	resetDB(t)

	outputs := runScenario(t, "testdata/scenarios/with_component.hcl", nil)

	merchantID := ctyInt(t, outputs["merchant_id"])
	productID := ctyInt(t, outputs["product_id"])

	assert.Equal(t, "Globex", ctyString(t, outputs["merchant_name"]))

	var merchantCount int
	err := testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM merchants WHERE id = $1 AND name = 'Globex'", merchantID).
		Scan(&merchantCount)
	require.NoError(t, err)
	assert.Equal(t, 1, merchantCount)

	var (
		productMerchantID int64
		productName       string
	)
	err = testPool.QueryRow(context.Background(),
		"SELECT merchant_id, name FROM products WHERE id = $1", productID).
		Scan(&productMerchantID, &productName)
	require.NoError(t, err)
	assert.Equal(t, merchantID, productMerchantID)
	assert.Equal(t, "Gadget", productName)
}

func TestResetDB_IsolatesTests(t *testing.T) {
	resetDB(t)

	_ = runScenario(t, "testdata/scenarios/basic.hcl", nil)

	var before int
	require.NoError(t, testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM merchants").Scan(&before))
	assert.Equal(t, 1, before)

	resetDB(t)

	var after int
	require.NoError(t, testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM merchants").Scan(&after))
	assert.Equal(t, 0, after)
}
