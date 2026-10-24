package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/provider"
)

// SQLite needs no server, so unlike the postgres provider these tests
// exercise real execution against :memory: and temp-file databases.

func TestSchemaLoadsFromEmbeddedHCL(t *testing.T) {
	s := New().Schema()
	if s.Name != "sqlite" {
		t.Fatalf("name: got %q want sqlite", s.Name)
	}
	if s.Config["path"].Type != "string" || !s.Config["path"].Required || !s.Config["path"].Path {
		t.Fatalf("config.path: %+v", s.Config["path"])
	}
	if s.Actions["sqlite_query"].Attrs["query"].Type != "string" {
		t.Fatalf("action.sqlite_query.query: %+v", s.Actions["sqlite_query"].Attrs["query"])
	}
}

func TestConfigureValidatesPath(t *testing.T) {
	cases := []struct {
		name   string
		config map[string]cty.Value
		want   string
	}{
		{"missing", nil, "path is required"},
		{"null", map[string]cty.Value{"path": cty.NullVal(cty.String)}, "path is required"},
		{"wrong type", map[string]cty.Value{"path": cty.NumberIntVal(5)}, "must be a string"},
		{"empty", map[string]cty.Value{"path": cty.StringVal("")}, "must not be empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := New().Configure(context.Background(), c.config)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("expected %q error, got %v", c.want, err)
			}
		})
	}
}

func TestExecuteBeforeConfigure(t *testing.T) {
	_, err := New().Execute(context.Background(), "sqlite_query", map[string]cty.Value{})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected not-configured error, got %v", err)
	}
}

func TestExecuteUnknownAction(t *testing.T) {
	p := open(t, ":memory:")
	_, err := p.Execute(context.Background(), "imaginary", map[string]cty.Value{})
	if err == nil || !strings.Contains(err.Error(), `unknown action "imaginary"`) {
		t.Fatalf("expected unknown-action error, got %v", err)
	}
}

func TestCloseIdempotent(t *testing.T) {
	p := New()
	if err := p.Close(); err != nil {
		t.Fatalf("Close on unconfigured provider: %v", err)
	}
	p = open(t, ":memory:")
	if err := p.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestDSNEscapesURIMetacharacters(t *testing.T) {
	got := dsn("data/odd?name#1%.db")
	want := "file:data/odd%3fname%231%25.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestQueryReturningRow(t *testing.T) {
	p := open(t, ":memory:")
	exec(t, p, schemaSQL)

	out := exec(t, p, "INSERT INTO merchants (name, region) VALUES ('Acme', 'us') RETURNING id, name")
	if !out["id"].RawEquals(cty.NumberIntVal(1)) || !out["name"].RawEquals(cty.StringVal("Acme")) {
		t.Fatalf("outputs: %#v", out)
	}
	if _, ok := out["last_insert_id"]; ok {
		t.Fatal("a statement that returns rows should not report last_insert_id")
	}
}

func TestQueryWithoutRowsReportsInsertIDAndAffected(t *testing.T) {
	p := open(t, ":memory:")
	exec(t, p, schemaSQL)

	out := exec(t, p, "INSERT INTO merchants (name, region) VALUES ('Acme', 'us')")
	if !out["last_insert_id"].RawEquals(cty.NumberIntVal(1)) || !out["rows_affected"].RawEquals(cty.NumberIntVal(1)) {
		t.Fatalf("insert outputs: %#v", out)
	}

	exec(t, p, "INSERT INTO merchants (name, region) VALUES ('Globex', 'eu')")
	out = exec(t, p, "UPDATE merchants SET region = 'apac'")
	if !out["rows_affected"].RawEquals(cty.NumberIntVal(2)) {
		t.Fatalf("update rows_affected: %#v", out["rows_affected"])
	}
}

func TestQuerySelectNoRowsIsEmpty(t *testing.T) {
	p := open(t, ":memory:")
	exec(t, p, schemaSQL)
	if out := exec(t, p, "SELECT id FROM merchants WHERE id = 42"); len(out) != 0 {
		t.Fatalf("expected no outputs, got %#v", out)
	}
}

// :memory: databases are per-connection; actions must all see the
// same one or a table created in one action vanishes in the next.
func TestMemoryDatabaseSharedAcrossActions(t *testing.T) {
	p := open(t, ":memory:")
	exec(t, p, schemaSQL)
	exec(t, p, "INSERT INTO merchants (name, region) VALUES ('Acme', 'us')")
	out := exec(t, p, "SELECT COUNT(*) AS n FROM merchants")
	if !out["n"].RawEquals(cty.NumberIntVal(1)) {
		t.Fatalf("count: %#v", out["n"])
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	p := open(t, ":memory:")
	exec(t, p, schemaSQL)
	_, err := p.Execute(context.Background(), "sqlite_query", map[string]cty.Value{
		"query": cty.StringVal("INSERT INTO products (merchant_id, name) VALUES (99, 'Orphan')"),
	})
	if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Fatalf("expected a foreign key violation, got %v", err)
	}
}

func TestFileDatabasePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")

	p := open(t, path)
	exec(t, p, schemaSQL)
	exec(t, p, "INSERT INTO merchants (name, region) VALUES ('Acme', 'us')")
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	out := exec(t, open(t, path), "SELECT name FROM merchants")
	if !out["name"].RawEquals(cty.StringVal("Acme")) {
		t.Fatalf("row did not persist: %#v", out)
	}
}

func TestQueryErrorsAreWrapped(t *testing.T) {
	p := open(t, ":memory:")
	_, err := p.Execute(context.Background(), "sqlite_query", map[string]cty.Value{
		"query": cty.StringVal("SELEC nonsense"),
	})
	if err == nil || !strings.HasPrefix(err.Error(), "sqlite_query: execute:") {
		t.Fatalf("expected wrapped execute error, got %v", err)
	}
}

const schemaSQL = `CREATE TABLE merchants (
	id     INTEGER PRIMARY KEY,
	name   TEXT NOT NULL,
	region TEXT NOT NULL
);
CREATE TABLE products (
	id          INTEGER PRIMARY KEY,
	merchant_id INTEGER NOT NULL REFERENCES merchants(id),
	name        TEXT NOT NULL
)`

// open returns a provider configured against path and closed at the
// end of the test.
func open(t *testing.T, path string) *Provider {
	t.Helper()
	p := New()
	if err := p.Configure(context.Background(), map[string]cty.Value{"path": cty.StringVal(path)}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// exec runs one sqlite_query and fails the test on error.
func exec(t *testing.T, p *Provider, query string) map[string]cty.Value {
	t.Helper()
	out, err := p.Execute(context.Background(), "sqlite_query", map[string]cty.Value{"query": cty.StringVal(query)})
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return out
}

// An update matching more rows than the restore limit records no
// restore, even when asked, so a broad update doesn't fill the record.
func TestUpdateRestoreLimit(t *testing.T) {
	p := open(t, ":memory:")
	exec(t, p, schemaSQL)
	exec(t, p, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 1001)
		INSERT INTO merchants (name, region) SELECT 'm' || i, 'us' FROM n`)

	update := func(where cty.Value) *provider.Invocation {
		t.Helper()
		attrs := map[string]cty.Value{
			"table": cty.StringVal("merchants"),
			"set":   cty.ObjectVal(map[string]cty.Value{"region": cty.StringVal("eu")}),
			"where": where,
		}
		_, inv, err := p.ExecuteUndo(context.Background(), "sqlite_update", attrs, provider.InverseOptions{Requested: true})
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	if inv := update(cty.ObjectVal(map[string]cty.Value{"region": cty.StringVal("us")})); inv != nil {
		t.Fatalf("1001 rows: expected no restore, got %v", inv.Action)
	}
	inv := update(cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("m1")}))
	if inv == nil || inv.Action != "sqlite_restore" || inv.Attrs["rows"].LengthInt() != 1 {
		t.Fatalf("one row: expected a restore of it, got %#v", inv)
	}
}

// A typed insert into a cached table, concurrent with one into a table
// created after the schema was read (so it re-reads the schema), must
// not deadlock on SQLite's single connection: re-reading the schema
// mustn't hold the lock the other insert needs while it holds the
// connection.
func TestConcurrentInsertDuringSchemaRefresh(t *testing.T) {
	p := open(t, ":memory:")
	exec(t, p, schemaSQL)
	insert := func(table string) error {
		_, _, err := p.ExecuteUndo(context.Background(), "sqlite_insert", map[string]cty.Value{
			"table":  cty.StringVal(table),
			"values": cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("x"), "region": cty.StringVal("us")}),
		}, provider.InverseOptions{})
		return err
	}
	if err := insert("merchants"); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		for i := range 50 {
			table := fmt.Sprintf("later_%d", i)
			if _, err := p.Execute(context.Background(), "sqlite_query", map[string]cty.Value{
				"query": cty.StringVal("CREATE TABLE " + table + " (id INTEGER PRIMARY KEY, name TEXT, region TEXT)"),
			}); err != nil {
				done <- err
				return
			}
			var wg sync.WaitGroup
			errs := make([]error, 2)
			for j, tbl := range []string{"merchants", table} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					errs[j] = insert(tbl)
				}()
			}
			wg.Wait()
			for _, err := range errs {
				if err != nil {
					done <- err
					return
				}
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("deadlocked")
	}
}
