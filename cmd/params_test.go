package cmd

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestQueryParamsScenario: params from variables and another action's
// outputs reach the database as bound values, through plan and run.
func TestQueryParamsScenario(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "shop.db")
	createDB(t, db, "CREATE TABLE merchants (id INTEGER PRIMARY KEY, name TEXT NOT NULL, parent INTEGER)")
	scenario := filepath.Join(dir, "params.hcl")
	writeFile(t, scenario, `scenario "params" {
  variable "path" {}
  variable "name" { default = "O'Brien \"Ltd\"" }
  required_providers {
    db = { source = "builtin/sqlite" }
  }
  provider "db" { path = var.path }
  action "sqlite_query" "parent" {
    query  = "INSERT INTO merchants (name) VALUES (?) RETURNING id"
    params = [var.name]
  }
  action "sqlite_query" "child" {
    query  = "INSERT INTO merchants (name, parent) VALUES (?, ?) RETURNING id"
    params = ["child", action.sqlite_query.parent.id]
  }
}
`)
	if out, err := runCLI(t, "plan", scenario, "--no-color", "--var", "path="+db); err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	runRecorded(t, scenario, "--var", "path="+db)

	conn, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var name string
	if err := conn.QueryRow("SELECT p.name FROM merchants c JOIN merchants p ON p.id = c.parent WHERE c.name = 'child'").Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != `O'Brien "Ltd"` {
		t.Fatalf("parent name = %q", name)
	}
}
