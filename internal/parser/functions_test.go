package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// TestLoadScenario_FunctionsInVariableDefaults: stdlib HCL functions
// (upper, concat, jsonencode, ...) are available in variable default
// expressions. Without this, authors can't express computed defaults
// and the restriction bleeds into every downstream use of var.*.
func TestLoadScenario_FunctionsInVariableDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "s" {
  variable "name" {
    default = upper("acme")
  }
  variable "tags" {
    default = concat(["a", "b"], ["c"])
  }
  variable "payload" {
    default = jsonencode({ k = 1 })
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ps, err := LoadScenario(path, "", nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}

	if got := ps.Variables["name"].AsString(); got != "ACME" {
		t.Errorf("upper: got %q want %q", got, "ACME")
	}
	if got := ps.Variables["tags"].LengthInt(); got != 3 {
		t.Errorf("concat length: got %d want 3", got)
	}
	payload := ps.Variables["payload"]
	if payload.Type() != cty.String {
		t.Fatalf("jsonencode should return string, got %s", payload.Type().FriendlyName())
	}
	if !strings.Contains(payload.AsString(), `"k":1`) {
		t.Errorf("jsonencode output: %q", payload.AsString())
	}
}

// TestLoadScenario_FunctionsInProviderConfig: provider config
// expressions should be able to call functions too, not just
// dereference var.*.
func TestLoadScenario_FunctionsInProviderConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "s" {
  required_providers {
    postgres = { source = "builtin/postgres" }
  }
  variable "host" { default = "localhost" }
  provider "postgres" {
    dsn = format("postgres://%s:5432/x", var.host)
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ps, err := LoadScenario(path, "", nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	dsn := ps.ProviderConfigs["postgres"]["dsn"].AsString()
	if dsn != "postgres://localhost:5432/x" {
		t.Errorf("format in provider config: got %q", dsn)
	}
}
