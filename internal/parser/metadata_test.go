package parser

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadScenario_DescriptionAndTags: the optional description and
// tags attributes on a scenario block decode into the parsed struct
// and are ignored by execution paths. Absence is fine — both fields
// are optional.
func TestLoadScenario_DescriptionAndTags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "annotated" {
  description = "seeds the demo tenant with failed payments"
  tags        = ["demo", "sales"]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ps, err := LoadScenario(path, nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	if got, want := ps.Scenario.Description, "seeds the demo tenant with failed payments"; got != want {
		t.Errorf("description:\n  got  %q\n  want %q", got, want)
	}
	if len(ps.Scenario.Tags) != 2 || ps.Scenario.Tags[0] != "demo" || ps.Scenario.Tags[1] != "sales" {
		t.Errorf("tags: got %v want [demo sales]", ps.Scenario.Tags)
	}
}

// TestLoadScenario_MetadataOptional: a scenario with no description
// or tags attribute still loads; the fields stay zero-valued.
func TestLoadScenario_MetadataOptional(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "bare" {
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ps, err := LoadScenario(path, nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	if ps.Scenario.Description != "" {
		t.Errorf("description should be empty, got %q", ps.Scenario.Description)
	}
	if len(ps.Scenario.Tags) != 0 {
		t.Errorf("tags should be empty, got %v", ps.Scenario.Tags)
	}
}
