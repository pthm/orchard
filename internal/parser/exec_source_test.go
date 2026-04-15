package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadScenario_ExecSourceResolvedToScenarioDir: `exec:./foo` in
// required_providers must be rewritten to an absolute path anchored
// at the scenario file's directory, regardless of the process cwd.
// Without this, a scenario at /some/path/s.hcl fails to find its
// provider binary when run from any other directory.
func TestLoadScenario_ExecSourceResolvedToScenarioDir(t *testing.T) {
	dir := t.TempDir()
	scenarioPath := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(scenarioPath, []byte(`
scenario "s" {
  required_providers {
    p = { source = "exec:./bin/p" }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ps, err := LoadScenario(scenarioPath, nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}

	wantAbs := filepath.Join(dir, "bin", "p")
	got := ps.RequiredProviders["p"].Source
	if !strings.HasPrefix(got, "exec:") {
		t.Fatalf("source lost exec: prefix: %q", got)
	}
	gotPath := strings.TrimPrefix(got, "exec:")
	if !filepath.IsAbs(gotPath) {
		t.Errorf("source path should be absolute: %q", gotPath)
	}
	if gotPath != wantAbs {
		t.Errorf("source path:\n  got  %q\n  want %q", gotPath, wantAbs)
	}
}

// TestLoadScenario_ExecSourceAbsolutePathPreserved: a caller who
// already provides an absolute path shouldn't have it mangled.
func TestLoadScenario_ExecSourceAbsolutePathPreserved(t *testing.T) {
	dir := t.TempDir()
	scenarioPath := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(scenarioPath, []byte(`
scenario "s" {
  required_providers {
    p = { source = "exec:/usr/local/bin/p" }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ps, err := LoadScenario(scenarioPath, nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}

	if got, want := ps.RequiredProviders["p"].Source, "exec:/usr/local/bin/p"; got != want {
		t.Errorf("absolute path mutated:\n  got  %q\n  want %q", got, want)
	}
}

// TestLoadScenario_ExecSourceEmptyPathRejected: `exec:` with no path
// should fail at parse time so the issue surfaces before any attempt
// to spawn the provider.
func TestLoadScenario_ExecSourceEmptyPathRejected(t *testing.T) {
	dir := t.TempDir()
	scenarioPath := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(scenarioPath, []byte(`
scenario "s" {
  required_providers {
    p = { source = "exec:" }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadScenario(scenarioPath, nil)
	if err == nil {
		t.Fatal("expected error for empty exec path")
	}
	if !strings.Contains(err.Error(), "requires a path") {
		t.Errorf("error should name the cause: %v", err)
	}
}
