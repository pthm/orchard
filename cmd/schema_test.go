package cmd

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/pthm/orchard/pkg/dbschema"
	"github.com/pthm/orchard/pkg/snapshot"
)

// TestSchemaPull drives `orchard schema pull` end to end against
// SQLite databases: the first pull writes a snapshot, a second is a
// no-op, a different database under the same alias is refused, and
// --force replaces it.
func TestSchemaPull(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".orchard"), 0o755); err != nil {
		t.Fatal(err)
	}
	appDB := filepath.Join(project, "app.db")
	otherDB := filepath.Join(project, "analytics.db")
	createDB(t, appDB, "CREATE TABLE merchants (id INTEGER PRIMARY KEY, name TEXT NOT NULL)")
	createDB(t, otherDB, "CREATE TABLE events (id INTEGER PRIMARY KEY)")

	scenario := filepath.Join(project, "scenarios", "dev.hcl")
	if err := os.MkdirAll(filepath.Dir(scenario), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, scenario, `scenario "dev" {
  required_providers {
    db = { source = "builtin/sqlite" }
  }
  variable "path" {}
  provider "db" {
    path = var.path
  }
  action "sqlite_query" "noop" {
    query = "SELECT 1"
  }
}
`)
	snapPath := filepath.Join(project, ".orchard", "schema", "db.json")

	// First pull writes the snapshot.
	out, err := runCLI(t, "schema", "pull", scenario, "--var", "path="+appDB)
	if err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	if !strings.Contains(out, "wrote") || !strings.Contains(out, "sqlite/app.db") {
		t.Fatalf("unexpected output: %s", out)
	}
	snap, err := snapshot.Read(snapPath)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Kind != dbschema.SnapshotKind || snap.Target != "sqlite/app.db" ||
		snap.Provider != (snapshot.Provider{Source: "builtin/sqlite", Alias: "db"}) {
		t.Fatalf("snapshot envelope: %+v", snap)
	}
	var body dbschema.Schema
	if err := json.Unmarshal(snap.Body, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body.Tables["merchants"]; !ok {
		t.Fatalf("snapshot is missing merchants: %v", body.TableNames())
	}
	if strings.Contains(string(snap.Body), project) {
		t.Fatal("snapshot must not contain the machine-specific database path")
	}
	first, _ := os.ReadFile(snapPath)

	// Pulling an unchanged schema leaves the file alone.
	out, err = runCLI(t, "schema", "pull", scenario, "--var", "path="+appDB)
	if err != nil || !strings.Contains(out, "unchanged") {
		t.Fatalf("second pull: %v\n%s", err, out)
	}
	if again, _ := os.ReadFile(snapPath); !bytes.Equal(first, again) {
		t.Fatal("an unchanged pull rewrote the snapshot")
	}

	// A different database under the same alias is refused...
	out, err = runCLI(t, "schema", "pull", scenario, "--var", "path="+otherDB)
	if err == nil || !strings.Contains(err.Error(), "is a snapshot of sqlite/app.db, not sqlite/analytics.db") {
		t.Fatalf("expected the overwrite safeguard, got %v\n%s", err, out)
	}

	// ...unless forced.
	out, err = runCLI(t, "schema", "pull", scenario, "--var", "path="+otherDB, "--force")
	if err != nil {
		t.Fatalf("forced pull: %v\n%s", err, out)
	}
	if snap, err = snapshot.Read(snapPath); err != nil || snap.Target != "sqlite/analytics.db" {
		t.Fatalf("forced pull should have replaced the snapshot: %+v %v", snap, err)
	}
}

// TestSchemaPullResolvesRelativeSQLitePath: a relative SQLite path is
// relative to the scenario file, so pulling from another directory
// captures the real database instead of creating an empty one there.
func TestSchemaPullResolvesRelativeSQLitePath(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".orchard"), 0o755); err != nil {
		t.Fatal(err)
	}
	createDB(t, filepath.Join(project, "dev.db"), "CREATE TABLE merchants (id INTEGER PRIMARY KEY)")
	scenario := filepath.Join(project, "scenarios", "dev.hcl")
	if err := os.MkdirAll(filepath.Dir(scenario), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, scenario, `scenario "dev" {
  required_providers {
    db = { source = "builtin/sqlite" }
  }
  provider "db" {
    path = "../dev.db"
  }
  action "sqlite_query" "noop" {
    query = "SELECT 1"
  }
}
`)

	elsewhere := t.TempDir()
	t.Chdir(elsewhere)
	if out, err := runCLI(t, "schema", "pull", scenario); err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	snap, err := snapshot.Read(filepath.Join(project, ".orchard", "schema", "db.json"))
	if err != nil {
		t.Fatal(err)
	}
	var body dbschema.Schema
	if err := json.Unmarshal(snap.Body, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body.Tables["merchants"]; !ok {
		t.Fatalf("pulled the wrong database: tables %v", body.TableNames())
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("pull created files in the working directory: %v", entries)
	}
}

func TestSchemaPullWarnsOnEmptyDatabase(t *testing.T) {
	dir := t.TempDir()
	scenario := filepath.Join(dir, "empty.hcl")
	writeFile(t, scenario, `scenario "empty" {
  required_providers {
    db = { source = "builtin/sqlite" }
  }
  variable "path" {}
  provider "db" {
    path = var.path
  }
  action "sqlite_query" "noop" {
    query = "SELECT 1"
  }
}
`)
	if err := os.MkdirAll(filepath.Join(dir, ".orchard"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, "schema", "pull", scenario, "--var", "path="+filepath.Join(dir, "new.db"))
	if err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	if !strings.Contains(out, `warning: sqlite/new.db has no tables; check that provider "db" points at the right database`) {
		t.Fatalf("expected an empty-database warning:\n%s", out)
	}
}

func TestSchemaPullWithoutSchemaSource(t *testing.T) {
	scenario := filepath.Join(t.TempDir(), "http.hcl")
	writeFile(t, scenario, `scenario "http_only" {
  required_providers {
    http = { source = "builtin/http" }
  }
  action "http_request" "ping" {
    url = "http://localhost/ping"
  }
}
`)
	if _, err := runCLI(t, "schema", "pull", scenario); err == nil || !strings.Contains(err.Error(), "no provider in this scenario can capture a schema") {
		t.Fatalf("expected a no-provider error, got %v", err)
	}
}

// runCLI runs the orchard CLI with args and returns its output. A
// fresh root command re-binds every flag to its default, so package
// flag variables don't leak between calls.
func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := NewRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func createDB(t *testing.T, path, ddl string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
