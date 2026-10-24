package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pthm/orchard/internal/record"
)

// TestInspect: inspect shows a run by ID or by path, with its nodes,
// outputs and recorded undo; a failed run shows its errors; an
// unknown ID names the directory it searched.
func TestInspect(t *testing.T) {
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
	writeFile(t, good, "scenario \"good\" {\n  "+head+"  output \"name\" { value = action.sqlite_insert.acme.name }\n}\n")
	bad := filepath.Join(project, "bad.hcl")
	writeFile(t, bad, "scenario \"bad\" {\n  "+head+`  action "sqlite_query" "broken" {
    query      = "NOT VALID SQL"
    depends_on = [action.sqlite_insert.acme]
  }
}
`)
	dir := t.TempDir()
	if out, err := runCLI(t, "run", good, "--no-color", "--record-dir", dir); err != nil {
		t.Fatalf("run good: %v\n%s", err, out)
	}
	files, err := record.List(dir, t.Output())
	if err != nil || len(files) != 1 {
		t.Fatalf("records: %v %v", files, err)
	}
	for _, arg := range []string{files[0].Record.ID, files[0].Path} {
		out, err := runCLI(t, "inspect", arg, "--record-dir", dir, "--no-color")
		if err != nil {
			t.Fatalf("inspect %s: %v\n%s", arg, err, out)
		}
		for _, want := range []string{
			"Scenario    good  (" + good + ")",
			"Status      succeeded",
			"succeeded  action.sqlite_insert.acme  ",
			`name = "Acme"`,
			`undo: sqlite_delete {`,
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("inspect %s: missing %q:\n%s", arg, want, out)
			}
		}
		if strings.Contains(out, "did not run") {
			t.Fatalf("succeeded run should not mention unrun nodes:\n%s", out)
		}
	}

	if out, err := runCLI(t, "run", bad, "--no-color", "--record-dir", dir); err == nil {
		t.Fatalf("run bad should fail:\n%s", out)
	}
	files, err = record.List(dir, t.Output())
	if err != nil || len(files) != 2 {
		t.Fatalf("records: %v %v", files, err)
	}
	out, err := runCLI(t, "inspect", files[0].Record.ID, "--record-dir", dir, "--no-color")
	if err != nil {
		t.Fatalf("inspect bad: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Status      failed",
		"  Error       ",
		"failed  action.sqlite_query.broken  ",
		"      error: ",
		"Nodes not listed did not run.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("inspect bad: missing %q:\n%s", want, out)
		}
	}

	if out, err := runCLI(t, "inspect", "20990101T000000Z_deadbeef", "--record-dir", dir); err == nil ||
		!strings.Contains(err.Error(), dir) {
		t.Fatalf("unknown ID should name %s: %v\n%s", dir, err, out)
	}
}
