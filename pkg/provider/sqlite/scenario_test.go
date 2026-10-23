package sqlite_test

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/internal/record"
	"github.com/pthm/orchard/pkg/provider"
	_ "github.com/pthm/orchard/pkg/provider/sqlite"
)

// TestScenarioRunAndTeardown drives a scenario through the full
// pipeline (parse, graph, plan validation, run with a run record,
// teardown from that record) against a temp-file database. It's the
// SQLite counterpart of test/integration's postgres teardown test,
// and needs no container.
//
// The product row references the merchant, and foreign keys are
// enforced, so teardown only succeeds if it deletes the product
// first.
func TestScenarioRunAndTeardown(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE merchants (id INTEGER PRIMARY KEY, name TEXT NOT NULL, region TEXT NOT NULL);
		CREATE TABLE products (id INTEGER PRIMARY KEY, merchant_id INTEGER NOT NULL REFERENCES merchants(id), name TEXT NOT NULL);
	`); err != nil {
		t.Fatal(err)
	}

	const scenario = "testdata/teardown.hcl"
	vars := map[string]string{"path": dbPath}

	// --- run, recording to a temp dir ---
	ps, g, eng := load(t, scenario, vars)
	if err := eng.ValidateReferences(ps, g); err != nil {
		t.Fatalf("plan validation: %v", err)
	}
	recordDir := t.TempDir()
	rep, err := record.NewReporter(record.Options{
		Writer:    record.FileWriter{Dir: recordDir},
		Scenario:  record.ScenarioFromParser(ps, scenario),
		Variables: ps.Variables,
		LogOutput: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	eng.SetReporter(rep)
	result, err := eng.Execute(context.Background(), ps, g)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.WriteErr != nil {
		t.Fatalf("record write: %v", rep.WriteErr)
	}

	// last_insert_id from the first action was wired into the second.
	merchantID := result.Outputs["merchant_id"]
	if !merchantID.RawEquals(cty.NumberIntVal(1)) {
		t.Fatalf("merchant_id: %#v", merchantID)
	}
	if !result.Outputs["product_merchant_id"].RawEquals(merchantID) {
		t.Fatalf("product_merchant_id %#v, want %#v", result.Outputs["product_merchant_id"], merchantID)
	}
	assertCount(t, db, "merchants", 1)
	assertCount(t, db, "products", 1)

	// --- teardown from the record on disk ---
	entries, err := os.ReadDir(recordDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one record file, got %v (%v)", entries, err)
	}
	rec, err := record.Load(filepath.Join(recordDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	recVars, err := record.DecodeCty(rec.Variables)
	if err != nil {
		t.Fatal(err)
	}
	recOutputs, err := record.IndexOutputs(rec)
	if err != nil {
		t.Fatal(err)
	}

	ps, g, eng = load(t, scenario, vars)
	if err := eng.Teardown(context.Background(), ps, g, recVars, recOutputs); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	assertCount(t, db, "products", 0)
	assertCount(t, db, "merchants", 0)
}

// load parses a scenario and builds a configured engine for it, the
// way cmd/run.go does.
func load(t *testing.T, path string, vars map[string]string) (*parser.ParsedScenario, *graph.Graph, *engine.Engine) {
	t.Helper()
	ps, err := parser.LoadScenario(path, "", vars)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	g, err := graph.Build(ps)
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	reg := provider.NewRegistry()
	reg.SetBaseDir(ps.ScenarioDir)
	for name, spec := range ps.RequiredProviders {
		p, err := provider.NewBuiltin(spec.Source)
		if err != nil {
			t.Fatal(err)
		}
		if err := reg.Register(name, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.Configure(context.Background(), ps.ProviderConfigs); err != nil {
		t.Fatalf("configure: %v", err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	eng, err := engine.New(reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.ValidateGraph(g); err != nil {
		t.Fatalf("validate graph: %v", err)
	}
	return ps, g, eng
}

func assertCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("%s: %d rows, want %d", table, n, want)
	}
}
