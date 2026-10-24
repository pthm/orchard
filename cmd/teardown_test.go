package cmd

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// runRecorded runs scenario with args and returns the record it wrote.
func runRecorded(t *testing.T, scenario string, args ...string) string {
	t.Helper()
	dir := t.TempDir()
	out, err := runCLI(t, append([]string{"run", scenario, "--no-color", "--record-dir", dir}, args...)...)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	recs, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(recs) != 1 {
		t.Fatalf("expected one record, got %v", recs)
	}
	return recs[0]
}

func countRows(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM merchants").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestTeardownUsesRecordedVariables: teardown reloads the scenario with
// the run's variables, so a variable without a default resolves and
// count expands to every instance the run created.
func TestTeardownUsesRecordedVariables(t *testing.T) {
	project := t.TempDir()
	db := filepath.Join(project, "shop.db")
	createDB(t, db, "CREATE TABLE merchants (id INTEGER PRIMARY KEY, name TEXT NOT NULL)")
	scenario := filepath.Join(project, "shop.hcl")
	writeFile(t, scenario, `scenario "shop" {
  variable "path" {}
  variable "n" {
    type    = number
    default = 1
  }
  required_providers {
    db = { source = "builtin/sqlite" }
  }
  provider "db" {
    path = var.path
  }
  action "sqlite_insert" "merchant" {
    count  = var.n
    table  = "merchants"
    values = { name = "m${count.index}" }
  }
}
`)
	rec := runRecorded(t, scenario, "--var", "path="+db, "--var", "n=3")
	if got := countRows(t, db); got != 3 {
		t.Fatalf("run should insert 3 rows, got %d", got)
	}
	if out, err := runCLI(t, "teardown", rec, "--no-color"); err != nil {
		t.Fatalf("teardown: %v\n%s", err, out)
	}
	if got := countRows(t, db); got != 0 {
		t.Fatalf("teardown should remove every row, %d left", got)
	}
}

// TestTeardownRefusesMissingNodes: a recorded node that created
// something but has since left the scenario fails teardown by name
// rather than being skipped.
func TestTeardownRefusesMissingNodes(t *testing.T) {
	project := t.TempDir()
	createDB(t, filepath.Join(project, "shop.db"), "CREATE TABLE merchants (id INTEGER PRIMARY KEY, name TEXT NOT NULL)")
	scenario := filepath.Join(project, "shop.hcl")
	const head = `scenario "shop" {
  required_providers {
    db = { source = "builtin/sqlite" }
  }
  provider "db" {
    path = "./shop.db"
  }
  action "sqlite_insert" "acme" {
    table  = "merchants"
    values = { name = "Acme" }
  }
`
	writeFile(t, scenario, head+`  action "sqlite_insert" "globex" {
    table  = "merchants"
    values = { name = "Globex" }
  }
}
`)
	rec := runRecorded(t, scenario)
	writeFile(t, scenario, head+"}\n")

	out, err := runCLI(t, "teardown", rec, "--no-color")
	if err == nil || !strings.Contains(err.Error(), "action.sqlite_insert.globex") ||
		strings.Contains(err.Error(), "action.sqlite_insert.acme") {
		t.Fatalf("teardown should name only the missing node: %v\n%s", err, out)
	}
	if got := countRows(t, filepath.Join(project, "shop.db")); got != 2 {
		t.Fatalf("a refused teardown should leave rows alone, %d left", got)
	}
}
