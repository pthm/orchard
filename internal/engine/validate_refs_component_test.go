package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/provider"
)

// validateComponentRefs parses a scenario + inline component pair
// and runs ValidateReferences against them. The component file is
// named "comp.hcl" alongside the scenario so `source = "./comp.hcl"`
// in the scenario resolves to it.
func validateComponentRefs(t *testing.T, compSrc, scenarioSrc string, providers map[string]provider.Provider) error {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "comp.hcl"), []byte(compSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "scenario.hcl")
	if err := os.WriteFile(path, []byte(scenarioSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, err := parser.LoadScenario(path, nil)
	if err != nil {
		return err
	}
	g, err := graph.Build(ps)
	if err != nil {
		return err
	}
	reg := provider.NewRegistry()
	for name, p := range providers {
		if err := reg.Register(name, p); err != nil {
			t.Fatal(err)
		}
	}
	eng, err := New(reg)
	if err != nil {
		return err
	}
	return eng.ValidateReferences(ps, g)
}

// TestValidateRefsComponentOutputTypo: a typo in a component output
// expression must surface at plan time, not after execute. The
// typed context is scoped to the component, so var.* and action.*
// only see that component's own declarations.
func TestValidateRefsComponentOutputTypo(t *testing.T) {
	comp := `
component "c" {
  variable "name" {
    type    = string
    default = "x"
  }
  action "fetch" "inner" {
    url = var.name
  }
  output "bad" {
    value = action.fetch.inner.bdy
  }
}`
	scenario := `
scenario "s" {
  required_providers {
    tp = { source = "builtin/tp" }
  }
  component "c" "one" {
    source = "./comp.hcl"
  }
}`
	err := validateComponentRefs(t, comp, scenario, map[string]provider.Provider{"tp": typedOutputProvider{}})
	if err == nil {
		t.Fatal("expected typo in component output to fail plan")
	}
	if !strings.Contains(err.Error(), "bdy") {
		t.Errorf("error should name the typo: %v", err)
	}
	if !strings.Contains(err.Error(), `output "bad"`) {
		t.Errorf("error should locate the output: %v", err)
	}
}

// TestValidateRefsComponentOutputValid: a correctly-typed reference
// on a component output passes the check cleanly.
func TestValidateRefsComponentOutputValid(t *testing.T) {
	comp := `
component "c" {
  action "fetch" "inner" {
    url = "/x"
  }
  output "status" {
    value = action.fetch.inner.status
  }
  output "body" {
    value = action.fetch.inner.body
  }
}`
	scenario := `
scenario "s" {
  required_providers {
    tp = { source = "builtin/tp" }
  }
  component "c" "one" {
    source = "./comp.hcl"
  }
}`
	err := validateComponentRefs(t, comp, scenario, map[string]provider.Provider{"tp": typedOutputProvider{}})
	if err != nil {
		t.Fatalf("valid component outputs should pass: %v", err)
	}
}

// TestValidateRefsComponentVarReference: a component output that
// references var.<name> resolves through the component's declared
// variables at plan time.
func TestValidateRefsComponentVarReference(t *testing.T) {
	comp := `
component "c" {
  variable "prefix" {
    type    = string
    default = "/x"
  }
  action "fetch" "inner" {
    url = var.prefix
  }
  output "echoed_prefix" {
    value = var.prefix
  }
}`
	scenario := `
scenario "s" {
  required_providers {
    tp = { source = "builtin/tp" }
  }
  component "c" "one" {
    source = "./comp.hcl"
  }
}`
	err := validateComponentRefs(t, comp, scenario, map[string]provider.Provider{"tp": typedOutputProvider{}})
	if err != nil {
		t.Fatalf("var.prefix reference should pass: %v", err)
	}
}

// TestValidateRefsComponentUndeclaredVar: referencing var.<unknown>
// from inside a component output surfaces as an unknown-variable
// error. The var namespace is scoped to the component's own
// variable blocks.
func TestValidateRefsComponentUndeclaredVar(t *testing.T) {
	comp := `
component "c" {
  variable "declared" {
    type    = string
    default = "ok"
  }
  action "fetch" "inner" {
    url = var.declared
  }
  output "bad" {
    value = var.nope
  }
}`
	scenario := `
scenario "s" {
  required_providers {
    tp = { source = "builtin/tp" }
  }
  component "c" "one" {
    source = "./comp.hcl"
  }
}`
	err := validateComponentRefs(t, comp, scenario, map[string]provider.Provider{"tp": typedOutputProvider{}})
	if err == nil {
		t.Fatal("expected undeclared-var error")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error should name the unknown var: %v", err)
	}
}
