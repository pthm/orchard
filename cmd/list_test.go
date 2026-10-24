package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pthm/orchard/internal/record"
)

// TestListRuns: runs are listed newest first with their status, a
// teardown marks only the record it tore down, and an empty record
// dir says where it looked.
func TestListRuns(t *testing.T) {
	project := t.TempDir()
	createDB(t, filepath.Join(project, "shop.db"), "CREATE TABLE merchants (id INTEGER PRIMARY KEY, name TEXT NOT NULL)")
	const head = `required_providers {
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
	good := filepath.Join(project, "good.hcl")
	writeFile(t, good, "scenario \"good\" {\n  "+head+"}\n")
	bad := filepath.Join(project, "bad.hcl")
	writeFile(t, bad, "scenario \"bad\" {\n  "+head+`  action "sqlite_query" "broken" {
    query      = "NOT VALID SQL"
    depends_on = [action.sqlite_insert.acme]
  }
}
`)

	dir := t.TempDir()
	if out, err := runCLI(t, "list", "runs", "--record-dir", dir, "--no-color"); err != nil ||
		!strings.Contains(out, "No runs found in "+dir+" (see --record-dir)") {
		t.Fatalf("empty dir: %v\n%s", err, out)
	}

	if out, err := runCLI(t, "run", good, "--no-color", "--record-dir", dir); err != nil {
		t.Fatalf("run good: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "run", bad, "--no-color", "--record-dir", dir); err == nil {
		t.Fatalf("run bad should fail:\n%s", out)
	}

	out, err := runCLI(t, "list", "runs", "--record-dir", dir, "--no-color")
	if err != nil {
		t.Fatalf("list runs: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two runs:\n%s", out)
	}
	if !strings.Contains(lines[0], "  bad  failed  ") || !strings.Contains(lines[0], "1 succeeded, 1 failed") ||
		!strings.Contains(lines[1], "  good  succeeded  ") || strings.Contains(out, "torn down") {
		t.Fatalf("want bad (failed) then good (succeeded), nothing torn down:\n%s", out)
	}

	files, err := record.List(dir, t.Output())
	if err != nil {
		t.Fatal(err)
	}
	goodID := files[1].Record.ID
	if out, err := runCLI(t, "teardown", goodID, "--record-dir", dir, "--no-color"); err != nil {
		t.Fatalf("teardown: %v\n%s", err, out)
	}
	out, err = runCLI(t, "list", "runs", "--record-dir", dir, "--no-color")
	if err != nil {
		t.Fatalf("list runs: %v\n%s", err, out)
	}
	lines = strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || strings.Contains(lines[0], "torn down") ||
		!strings.Contains(lines[1], goodID+"  good  succeeded  torn down  ") {
		t.Fatalf("only the good run should show torn down:\n%s", out)
	}
}

// TestListScenarios: `orchard list <dir>` still lists scenarios now
// that list has a runs subcommand.
func TestListScenarios(t *testing.T) {
	project := t.TempDir()
	writeFile(t, filepath.Join(project, "s.hcl"), `scenario "hello" {
  description = "Say hello"
}
`)
	out, err := runCLI(t, "list", project)
	if err != nil || !strings.Contains(out, "hello") || !strings.Contains(out, "Say hello") {
		t.Fatalf("list scenarios: %v\n%s", err, out)
	}
}
