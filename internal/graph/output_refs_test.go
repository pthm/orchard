package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pthm/orchard/internal/parser"
)

// buildFromScenario parses a scenario HCL snippet and returns the
// graph or an error. Tests use this to drive full graph.Build so the
// new output-reference validation runs on realistic input.
func buildFromScenario(t *testing.T, src string) (*Graph, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.hcl")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	ps, err := parser.LoadScenario(path, "", nil)
	if err != nil {
		return nil, err
	}
	return Build(ps)
}

func TestOutputReferenceToMissingActionRejected(t *testing.T) {
	_, err := buildFromScenario(t, `
scenario "bad_output" {
  action "sql" "real" {
    query = "SELECT 1"
  }
  output "x" {
    value = action.sql.missing.id
  }
}`)
	if err == nil {
		t.Fatal("expected error for output referencing missing action")
	}
	if !strings.Contains(err.Error(), "missing") || !strings.Contains(err.Error(), "output") {
		t.Fatalf("error should name the offending output and reference: %v", err)
	}
}

func TestOutputReferenceToRealActionPasses(t *testing.T) {
	_, err := buildFromScenario(t, `
scenario "good_output" {
  action "sql" "real" {
    query = "SELECT 1"
  }
  output "x" {
    value = action.sql.real.id
  }
}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOutputReferenceToComponentPasses(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "c.hcl"), []byte(`
component "m" {
  output "id" { value = 1 }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s.hcl"), []byte(`
scenario "s" {
  component "m" "base" { source = "./c.hcl" }
  output "x" { value = component.m.base.id }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, err := parser.LoadScenario(filepath.Join(dir, "s.hcl"), "", nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := Build(ps); err != nil {
		t.Fatalf("Build: %v", err)
	}
}
