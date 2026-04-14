package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTempScenario writes src to a temp .hcl file and returns its
// path. Cleaned up via t.Cleanup so each test stays hermetic.
func writeTempScenario(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.hcl")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestDuplicateScenarioVariable(t *testing.T) {
	path := writeTempScenario(t, `
scenario "dup_var" {
  variable "name" { default = "a" }
  variable "name" { default = "b" }
}`)
	_, err := LoadScenario(path, nil)
	if err == nil || !strings.Contains(err.Error(), "duplicate variable") {
		t.Fatalf("expected duplicate-variable error, got %v", err)
	}
}

func TestDuplicateScenarioAction(t *testing.T) {
	path := writeTempScenario(t, `
scenario "dup_action" {
  action "sql" "create" { query = "SELECT 1" }
  action "sql" "create" { query = "SELECT 2" }
}`)
	_, err := LoadScenario(path, nil)
	if err == nil || !strings.Contains(err.Error(), "duplicate action") {
		t.Fatalf("expected duplicate-action error, got %v", err)
	}
	// Actions of the same type but different names must still be allowed.
	pathOK := writeTempScenario(t, `
scenario "ok" {
  action "sql" "a" { query = "SELECT 1" }
  action "sql" "b" { query = "SELECT 2" }
}`)
	if _, err := LoadScenario(pathOK, nil); err != nil {
		t.Fatalf("distinct-name actions should parse: %v", err)
	}
}

func TestDuplicateScenarioOutput(t *testing.T) {
	path := writeTempScenario(t, `
scenario "dup_output" {
  output "x" { value = 1 }
  output "x" { value = 2 }
}`)
	_, err := LoadScenario(path, nil)
	if err == nil || !strings.Contains(err.Error(), "duplicate output") {
		t.Fatalf("expected duplicate-output error, got %v", err)
	}
}

func TestDuplicateComponentInstance(t *testing.T) {
	dir := t.TempDir()
	// A minimal component definition so the component refs can resolve.
	if err := os.WriteFile(
		filepath.Join(dir, "comp.hcl"),
		[]byte(`component "c" { output "x" { value = 1 } }`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "scenario.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "dup_component" {
  component "c" "base" { source = "./comp.hcl" }
  component "c" "base" { source = "./comp.hcl" }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadScenario(path, nil)
	if err == nil || !strings.Contains(err.Error(), "duplicate component") {
		t.Fatalf("expected duplicate-component error, got %v", err)
	}
}

func TestDuplicateInsideComponent(t *testing.T) {
	dir := t.TempDir()
	// Component with duplicate variable.
	if err := os.WriteFile(
		filepath.Join(dir, "comp.hcl"),
		[]byte(`
component "c" {
  variable "x" { default = 1 }
  variable "x" { default = 2 }
}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "scenario.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "s" {
  component "c" "i" { source = "./comp.hcl" }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadScenario(path, nil)
	if err == nil || !strings.Contains(err.Error(), "duplicate variable") {
		t.Fatalf("expected duplicate-variable-in-component error, got %v", err)
	}
}

func TestDuplicateProviderConfig(t *testing.T) {
	path := writeTempScenario(t, `
scenario "dup_provider" {
  required_providers {
    postgres = { source = "builtin/postgres" }
  }
  provider "postgres" { dsn = "one" }
  provider "postgres" { dsn = "two" }
}`)
	_, err := LoadScenario(path, nil)
	if err == nil || !strings.Contains(err.Error(), "duplicate provider config") {
		t.Fatalf("expected duplicate-provider-config error, got %v", err)
	}
}
