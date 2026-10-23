package integration_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/provider"
	"github.com/pthm/orchard/pkg/provider/external"

	// Register builtins. Side-effect imports mirror what cmd/run.go
	// does, so the integration runner sees the same provider set
	// as the real binary.
	_ "github.com/pthm/orchard/pkg/provider/http"
	_ "github.com/pthm/orchard/pkg/provider/mysql"
	_ "github.com/pthm/orchard/pkg/provider/postgres"
	_ "github.com/pthm/orchard/pkg/provider/redis"
	_ "github.com/pthm/orchard/pkg/provider/s3"
)

// runScenario loads, validates, and executes an HCL scenario through
// the full orchard pipeline and returns its outputs. It builds the
// registry the same way cmd/run.go does — reading required_providers
// from the scenario — so scenarios drive which providers are active
// instead of each test hard-coding a registry.
func runScenario(t *testing.T, path string, vars map[string]string) map[string]cty.Value {
	t.Helper()

	ps, err := parser.LoadScenario(path, "", vars)
	require.NoError(t, err, "loading scenario")

	g, err := graph.Build(ps)
	require.NoError(t, err, "building graph")

	reg, err := buildRegistry(ps)
	require.NoError(t, err, "building registry")
	t.Cleanup(func() {
		if err := reg.Close(); err != nil {
			t.Logf("registry close: %v", err)
		}
	})

	eng, err := engine.New(reg)
	require.NoError(t, err, "building engine")

	require.NoError(t, eng.ValidateGraph(g), "plan validation")

	result, err := eng.Execute(context.Background(), ps, g)
	require.NoError(t, err, "executing scenario")

	return result.Outputs
}

// buildRegistry mirrors cmd/registry.go for the integration tests:
// instantiate each required provider by source scheme, register, and
// run Configure with the evaluated provider config. Duplicating the
// shape here avoids importing cmd/ (which is typically not imported
// as a library) and keeps the integration harness independent.
func buildRegistry(ps *parser.ParsedScenario) (*provider.Registry, error) {
	reg := provider.NewRegistry()
	reg.SetBaseDir(ps.ScenarioDir)
	for name, spec := range ps.RequiredProviders {
		p, err := instantiateProvider(spec.Source)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", name, err)
		}
		if err := reg.Register(name, p); err != nil {
			return nil, fmt.Errorf("provider %q: %w", name, err)
		}
	}
	if err := reg.Configure(context.Background(), ps.ProviderConfigs); err != nil {
		return nil, err
	}
	return reg, nil
}

func instantiateProvider(source string) (provider.Provider, error) {
	switch {
	case strings.HasPrefix(source, "builtin/"):
		return provider.NewBuiltin(source)
	case strings.HasPrefix(source, "exec:"):
		return external.Spawn(strings.TrimPrefix(source, "exec:"))
	default:
		return nil, fmt.Errorf("unknown source scheme: %q", source)
	}
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

// --- postgres scenarios ----------------------------------------

func TestScenario_Basic(t *testing.T) {
	resetDB(t)

	outputs := runScenario(t, "testdata/scenarios/basic.hcl", map[string]string{
		"dsn": testDSN,
	})

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
		"dsn":           testDSN,
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

	outputs := runScenario(t, "testdata/scenarios/with_component.hcl", map[string]string{
		"dsn": testDSN,
	})

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

	_ = runScenario(t, "testdata/scenarios/basic.hcl", map[string]string{
		"dsn": testDSN,
	})

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

// --- external echo provider -----------------------------------

func TestScenario_Echo(t *testing.T) {
	outputs := runScenario(t, "testdata/scenarios/echo.hcl", nil)

	assert.Equal(t, "hello from integration test", ctyString(t, outputs["first"]))
	assert.Equal(t,
		"chained: hello from integration test",
		ctyString(t, outputs["second"]))
}

// --- aliased provider ----------------------------------------

// TestScenario_AliasedProvider exercises the review-#1 fix: the
// scenario registers postgres under the local alias "db". Without
// the alias-based routing change, Configure would be called with
// an empty config map and the postgres provider would fail with
// "dsn is required" even though the scenario supplied it.
func TestScenario_AliasedProvider(t *testing.T) {
	resetDB(t)
	outputs := runScenario(t, "testdata/scenarios/aliased.hcl", map[string]string{
		"dsn": testDSN,
	})
	if got := ctyInt(t, outputs["ok"]); got != 1 {
		t.Fatalf("ok: got %d want 1", got)
	}
}

// --- cross-provider + foreign-key dependency chain -----------

// TestScenario_CombinedProviders exercises the dependency graph
// across two providers and a multi-level FK chain:
//
//	echo.randomize (external, produces merchant name)
//	  → sql.merchant (postgres INSERT)
//	     → sql.product_a (FK merchants.id)
//	     → sql.product_b (FK merchants.id, sibling of product_a)
//	        → sql.review (FK products.id AND merchants.id — diamond)
//
// Assertions verify (a) the graph ordered everything correctly (FK
// constraints would reject out-of-order inserts), (b) the echo
// output flowed into the SQL chain, (c) sibling products are
// ordered independently of each other, and (d) the row in
// product_reviews points at product_a and merchant rows the
// scenario actually produced.
func TestScenario_CombinedProviders(t *testing.T) {
	resetDB(t)

	outputs := runScenario(t, "testdata/scenarios/combined.hcl", map[string]string{
		"dsn":             testDSN,
		"merchant_prefix": "acme",
	})

	merchantID := ctyInt(t, outputs["merchant_id"])
	productAID := ctyInt(t, outputs["product_a_id"])
	productBID := ctyInt(t, outputs["product_b_id"])
	reviewID := ctyInt(t, outputs["review_id"])

	assert.Positive(t, merchantID)
	assert.Positive(t, productAID)
	assert.Positive(t, productBID)
	assert.Positive(t, reviewID)
	assert.NotEqual(t, productAID, productBID, "sibling products must be distinct rows")

	// Name came from echo.randomize: "<prefix>-<8 hex chars>"
	merchantName := ctyString(t, outputs["merchant_name"])
	assert.Truef(t,
		strings.HasPrefix(merchantName, "acme-"),
		"merchant name %q should begin with the echo prefix", merchantName)
	assert.Lenf(t, merchantName, len("acme-")+8,
		"merchant name %q should carry an 8-char hex suffix", merchantName)

	// Verify the merchant row actually exists with that name.
	var dbName string
	require.NoError(t, testPool.QueryRow(context.Background(),
		"SELECT name FROM merchants WHERE id = $1", merchantID).Scan(&dbName))
	assert.Equal(t, merchantName, dbName)

	// Both products FK the merchant.
	var productMerchantCount int
	require.NoError(t, testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM products WHERE merchant_id = $1 AND id IN ($2, $3)",
		merchantID, productAID, productBID).Scan(&productMerchantCount))
	assert.Equal(t, 2, productMerchantCount, "both products must link to the merchant")

	// The review FKs both product_a and the merchant.
	var (
		reviewProductID  int64
		reviewMerchantID int64
		reviewRating     int
	)
	require.NoError(t, testPool.QueryRow(context.Background(),
		"SELECT product_id, merchant_id, rating FROM product_reviews WHERE id = $1", reviewID).
		Scan(&reviewProductID, &reviewMerchantID, &reviewRating))
	assert.Equal(t, productAID, reviewProductID)
	assert.Equal(t, merchantID, reviewMerchantID)
	assert.Equal(t, 5, reviewRating)

	// Successful FK inserts are themselves proof of correct topo
	// order — a sibling that ran before its parent would have
	// failed here with a foreign-key-violation error, not made it
	// into outputs.
}

// --- http builtin provider ------------------------------------

func TestScenario_HTTP(t *testing.T) {
	// Stand up a local HTTP server that the scenario points at via
	// the base_url variable. The handler recognizes both routes
	// referenced by the scenario and records some request metadata
	// so the assertions can verify headers and bodies propagate.
	var (
		fetchTestHeader  string
		submitMethod     string
		submitBody       []byte
		submitContentTyp string
	)
	srv := httptest.NewServer(&httpFixture{
		onFetch: func(r *httpFixtureRequest) httpFixtureResponse {
			fetchTestHeader = r.headers["X-Test-Case"]
			return httpFixtureResponse{status: 200, body: "pong"}
		},
		onSubmit: func(r *httpFixtureRequest) httpFixtureResponse {
			submitMethod = r.method
			submitBody = r.body
			submitContentTyp = r.headers["Content-Type"]
			return httpFixtureResponse{status: 201, body: "created"}
		},
	})
	defer srv.Close()

	outputs := runScenario(t, "testdata/scenarios/http.hcl", map[string]string{
		"base_url": srv.URL,
	})

	// Response assertions via scenario outputs.
	assert.Equal(t, int64(200), ctyInt(t, outputs["fetch_status"]))
	assert.Equal(t, "pong", ctyString(t, outputs["fetch_body"]))
	assert.Equal(t, int64(201), ctyInt(t, outputs["submit_status"]))

	// Request-side assertions — verifies per-request and default
	// headers merged, and POST with body round-tripped.
	assert.Equal(t, "fetch", fetchTestHeader)
	assert.Equal(t, "POST", submitMethod)
	assert.Equal(t, `{"hello":"world"}`, string(submitBody))
	assert.Equal(t, "application/json", submitContentTyp)
}
