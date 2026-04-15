package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMultiScenario_PickByName: a file with several scenario blocks
// loads the one matching the explicit name argument.
func TestMultiScenario_PickByName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "alpha" {
  variable "name" { default = "alpha" }
}

scenario "beta" {
  variable "name" { default = "beta" }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"alpha", "beta"} {
		ps, err := LoadScenario(path, want, nil)
		if err != nil {
			t.Fatalf("LoadScenario(%q): %v", want, err)
		}
		if ps.Scenario.Name != want {
			t.Errorf("picked scenario: got %q want %q", ps.Scenario.Name, want)
		}
		if got := ps.Variables["name"].AsString(); got != want {
			t.Errorf("variable default: got %q want %q", got, want)
		}
	}
}

// TestMultiScenario_EmptyNameAmbiguous: with multiple scenarios and
// no explicit name, loading fails with an error listing the names so
// the caller knows what to pass to --scenario.
func TestMultiScenario_EmptyNameAmbiguous(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "alpha" {}
scenario "beta"  {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadScenario(path, "", nil)
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	for _, want := range []string{"multiple scenarios", "alpha", "beta"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

// TestMultiScenario_UnknownName: explicit name that doesn't exist
// fails with a listing of what's actually in the file.
func TestMultiScenario_UnknownName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "alpha" {}
scenario "beta"  {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadScenario(path, "gamma", nil)
	if err == nil {
		t.Fatal("expected not-found error")
	}
	for _, want := range []string{`"gamma"`, "alpha", "beta"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

// TestMultiScenario_DuplicateNameRejected: two scenarios with the
// same name make --scenario ambiguous even when specified; parser
// rejects the file at load.
func TestMultiScenario_DuplicateNameRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "dup" {}
scenario "dup" {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadScenario(path, "dup", nil)
	if err == nil {
		t.Fatal("expected duplicate-name error")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention duplication: %v", err)
	}
}

// TestMultiScenario_SingleImplicitEmptyName: a file with exactly one
// scenario and an empty name argument still loads — the common case
// for `orchard run foo.hcl` shouldn't require --scenario when it's
// unambiguous.
func TestMultiScenario_SingleImplicitEmptyName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "sole" {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ps, err := LoadScenario(path, "", nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	if ps.Scenario.Name != "sole" {
		t.Errorf("expected sole, got %q", ps.Scenario.Name)
	}
}
