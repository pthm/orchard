package cmd

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pthm/orchard/internal/record"
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

// TestTeardownProgress: teardown by run ID records each node it tore
// down, a re-run after a partial failure skips the nodes already torn
// down (even once the scenario stops declaring them), and a fully
// torn-down record is refused before the scenario is even loaded.
func TestTeardownProgress(t *testing.T) {
	t.Run("rerun", func(t *testing.T) { testTeardownProgress(t, false) })
	t.Run("torn node dropped", func(t *testing.T) { testTeardownProgress(t, true) })
}

func testTeardownProgress(t *testing.T, dropSecond bool) {
	project := t.TempDir()
	count := filepath.Join(project, "count")
	marker := filepath.Join(project, "marker")
	scenario := filepath.Join(project, "s.hcl")
	// Reverse order tears down "second" first (it appends to count),
	// then "first", which fails until the marker exists.
	src := `scenario "progress" {
  required_providers {
    exec = { source = "builtin/exec" }
  }
  action "exec_run" "first" {
    command = "true"
    lifecycle {
      teardown {
        command = "test"
        args    = ["-e", "` + marker + `"]
      }
    }
  }
  action "exec_run" "second" {
    command    = "true"
    depends_on = [action.exec_run.first]
    lifecycle {
      teardown {
        command = "sh"
        args    = ["-c", "echo x >> ` + count + `"]
      }
    }
  }
}
`
	writeFile(t, scenario, src)
	dir := t.TempDir()
	out, err := runCLI(t, "run", scenario, "--no-color", "--record-dir", dir)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	recs, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(recs) != 1 || !strings.Contains(out, "record: "+recs[0]) {
		t.Fatalf("run should print the record path %v:\n%s", recs, out)
	}
	rec, err := record.Load(recs[0])
	if err != nil {
		t.Fatal(err)
	}

	if _, err := runCLI(t, "teardown", rec.ID, "--record-dir", dir, "--no-color"); err == nil {
		t.Fatal("first teardown should fail on the missing marker")
	}
	rec, err = record.Load(recs[0])
	if err != nil {
		t.Fatal(err)
	}
	torn := map[string]bool{}
	for _, n := range rec.Nodes {
		torn[n.ID] = n.TornDown
	}
	if !torn["action.exec_run.second"] || torn["action.exec_run.first"] || rec.TornDownAt != nil {
		t.Fatalf("partial teardown should mark only second: %+v", rec)
	}

	if dropSecond {
		cut := strings.Index(src, `  action "exec_run" "second"`)
		writeFile(t, scenario, src[:cut]+"}\n")
	}
	writeFile(t, marker, "")
	if out, err := runCLI(t, "teardown", rec.ID, "--record-dir", dir, "--no-color"); err != nil {
		t.Fatalf("second teardown: %v\n%s", err, out)
	}
	if got, _ := os.ReadFile(count); string(got) != "x\n" {
		t.Errorf("second's teardown should run once, count file: %q", got)
	}
	rec, err = record.Load(recs[0])
	if err != nil {
		t.Fatal(err)
	}
	if rec.TornDownAt == nil {
		t.Fatalf("complete teardown should set torn_down_at: %+v", rec)
	}

	// Without the scenario nothing can be configured, so the refusal
	// must come first.
	if err := os.Remove(scenario); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, "teardown", recs[0], "--no-color"); err == nil || !strings.Contains(err.Error(), "already torn down at") {
		t.Fatalf("torn-down record should be refused, got %v", err)
	}
}

// latestScenario writes a sqlite scenario file into a fresh project
// whose insert names its row after var.name; with broken a query
// after the insert fails the run.
func latestScenario(t *testing.T, broken bool) (scenario, db string) {
	t.Helper()
	project := t.TempDir()
	db = filepath.Join(project, "shop.db")
	createDB(t, db, "CREATE TABLE merchants (id INTEGER PRIMARY KEY, name TEXT NOT NULL)")
	src := `scenario "shop" {
  variable "name" {}
  required_providers {
    db = { source = "builtin/sqlite" }
  }
  provider "db" {
    path = "./shop.db"
  }
  action "sqlite_insert" "m" {
    table  = "merchants"
    values = { name = var.name }
  }
`
	if broken {
		src += `  action "sqlite_query" "broken" {
    query      = "NOT VALID SQL"
    depends_on = [action.sqlite_insert.m]
  }
`
	}
	scenario = filepath.Join(project, "shop.hcl")
	writeFile(t, scenario, src+"}\n")
	return scenario, db
}

func merchantNames(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var names sql.NullString
	if err := db.QueryRow("SELECT group_concat(name, ',') FROM (SELECT name FROM merchants ORDER BY id)").Scan(&names); err != nil {
		t.Fatal(err)
	}
	return names.String
}

// TestTeardownLatest: --latest tears down a scenario file's runs newest
// first, marking each record, until none are left.
func TestTeardownLatest(t *testing.T) {
	scenario, db := latestScenario(t, false)
	dir := t.TempDir()
	for _, name := range []string{"old", "new"} {
		if out, err := runCLI(t, "run", scenario, "--no-color", "--record-dir", dir, "--var", "name="+name); err != nil {
			t.Fatalf("run %s: %v\n%s", name, err, out)
		}
	}
	files, err := record.List(dir, t.Output())
	if err != nil || len(files) != 2 {
		t.Fatalf("records: %v %v", files, err)
	}

	out, err := runCLI(t, "teardown", scenario, "--latest", "--record-dir", dir, "--no-color")
	if err != nil {
		t.Fatalf("first teardown: %v\n%s", err, out)
	}
	if want := "tearing down run " + files[0].Record.ID + " (" + files[0].Path + ")"; !strings.Contains(out, want) {
		t.Fatalf("missing %q:\n%s", want, out)
	}
	if got := merchantNames(t, db); got != "old" {
		t.Fatalf("first teardown should remove only the newer row, left %q", got)
	}
	if rec, err := record.Load(files[0].Path); err != nil || rec.TornDownAt == nil {
		t.Fatalf("newer record should be marked torn down: %+v %v", rec, err)
	}

	if out, err := runCLI(t, "teardown", scenario, "--latest", "--record-dir", dir, "--no-color"); err != nil {
		t.Fatalf("second teardown: %v\n%s", err, out)
	}
	if got := merchantNames(t, db); got != "" {
		t.Fatalf("second teardown should remove the older row, left %q", got)
	}

	_, err = runCLI(t, "teardown", scenario, "--latest", "--record-dir", dir, "--no-color")
	if want := "no runs left to tear down for " + scenario + " in " + dir; err == nil || err.Error() != want {
		t.Fatalf("want %q, got %v", want, err)
	}
}

// TestTeardownLatestFailedRun: a failed run is still picked, and its
// successful insert undone.
func TestTeardownLatestFailedRun(t *testing.T) {
	scenario, db := latestScenario(t, true)
	dir := t.TempDir()
	if out, err := runCLI(t, "run", scenario, "--no-color", "--record-dir", dir, "--var", "name=x"); err == nil {
		t.Fatalf("run should fail:\n%s", out)
	}
	if got := merchantNames(t, db); got != "x" {
		t.Fatalf("failed run should have inserted, got %q", got)
	}
	// The broken query has no teardown, so teardown itself succeeds.
	if out, err := runCLI(t, "teardown", scenario, "--latest", "--record-dir", dir, "--no-color"); err != nil {
		t.Fatalf("teardown: %v\n%s", err, out)
	}
	if got := merchantNames(t, db); got != "" {
		t.Fatalf("teardown should remove the inserted row, left %q", got)
	}
}

// TestTeardownLatestMatching: runs of another scenario file in the same
// dir are ignored, a path through a symlinked directory still matches,
// and a file with runs of two scenarios needs --scenario.
func TestTeardownLatestMatching(t *testing.T) {
	scenario, db := latestScenario(t, false)
	other, otherDB := latestScenario(t, false)
	dir := t.TempDir()
	if out, err := runCLI(t, "run", scenario, "--no-color", "--record-dir", dir, "--var", "name=mine"); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "run", other, "--no-color", "--record-dir", dir, "--var", "name=other"); err != nil {
		t.Fatalf("run other: %v\n%s", err, out)
	}

	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Dir(scenario), link); err != nil {
		t.Fatal(err)
	}
	if out, err := runCLI(t, "teardown", filepath.Join(link, "shop.hcl"), "--latest", "--record-dir", dir, "--no-color"); err != nil {
		t.Fatalf("teardown through symlink: %v\n%s", err, out)
	}
	if got, gotOther := merchantNames(t, db), merchantNames(t, otherDB); got != "" || gotOther != "other" {
		t.Fatalf("teardown should undo only this file's run: mine %q, other %q", got, gotOther)
	}

	// A second scenario in the same file.
	src, err := os.ReadFile(scenario)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, scenario, string(src)+strings.Replace(string(src), `scenario "shop"`, `scenario "shop2"`, 1))
	for _, name := range []string{"shop", "shop2"} {
		if out, err := runCLI(t, "run", scenario, "--scenario", name, "--no-color", "--record-dir", dir, "--var", "name="+name); err != nil {
			t.Fatalf("run %s: %v\n%s", name, err, out)
		}
	}
	_, err = runCLI(t, "teardown", scenario, "--latest", "--record-dir", dir, "--no-color")
	if err == nil || !strings.Contains(err.Error(), "scenarios shop, shop2; pick one with --scenario") {
		t.Fatalf("want a --scenario error, got %v", err)
	}
	if out, err := runCLI(t, "teardown", scenario, "--latest", "--scenario", "shop", "--record-dir", dir, "--no-color"); err != nil {
		t.Fatalf("teardown --scenario shop: %v\n%s", err, out)
	}
	if got := merchantNames(t, db); got != "shop2" {
		t.Fatalf("--scenario shop should undo only shop's run, left %q", got)
	}
}
