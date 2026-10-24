package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pthm/orchard/internal/record"
)

// TestRunInterrupted: Ctrl-C mid-run still writes a record, and
// teardown --latest undoes what the run had created.
func TestRunInterrupted(t *testing.T) {
	project := t.TempDir()
	db := filepath.Join(project, "shop.db")
	createDB(t, db, "CREATE TABLE merchants (id INTEGER PRIMARY KEY, name TEXT NOT NULL)")
	started := filepath.Join(project, "started")
	scenario := filepath.Join(project, "shop.hcl")
	writeFile(t, scenario, `scenario "shop" {
  required_providers {
    db   = { source = "builtin/sqlite" }
    exec = { source = "builtin/exec" }
  }
  provider "db" {
    path = "./shop.db"
  }
  action "sqlite_insert" "merchant" {
    table  = "merchants"
    values = { name = "Acme" }
  }
  action "exec_run" "slow" {
    command    = "sh"
    args       = ["-c", "touch `+started+` && sleep 30"]
    depends_on = [action.sqlite_insert.merchant]
  }
}
`)
	dir := t.TempDir()
	var out bytes.Buffer
	root := NewRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"run", scenario, "--no-color", "--record-dir", dir})
	done := make(chan error, 1)
	go func() { done <- ExecuteWithSignals(root) }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the slow action never started:\n%s", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an interrupted run should fail")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not stop on SIGINT")
	}

	recs, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(recs) != 1 || !strings.Contains(out.String(), "record: "+recs[0]) {
		t.Fatalf("an interrupted run should write and print its record %v:\n%s", recs, out.String())
	}
	rec, err := record.Load(recs[0])
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != record.StatusFailed || len(rec.Nodes) != 2 ||
		rec.Nodes[0].Status != record.StatusSucceeded || rec.Nodes[0].Undo == nil {
		t.Fatalf("record should hold the insert with its undo: %+v", rec)
	}

	if out, err := runCLI(t, "teardown", scenario, "--latest", "--record-dir", dir, "--no-color"); err != nil {
		t.Fatalf("teardown: %v\n%s", err, out)
	}
	if n := countRows(t, db); n != 0 {
		t.Fatalf("teardown should delete the inserted row, %d left", n)
	}
}
