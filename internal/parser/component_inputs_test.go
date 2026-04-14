package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadWithComponent writes a component definition and a scenario
// that references it, then runs LoadScenario. Returns the error
// so tests can inspect plan-time validation failures.
func loadWithComponent(t *testing.T, compSrc, scenarioSrc string) error {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "comp.hcl"), []byte(compSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "scenario.hcl")
	if err := os.WriteFile(path, []byte(scenarioSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadScenario(path, nil)
	return err
}

func TestPlanRejectsUndeclaredComponentInput(t *testing.T) {
	err := loadWithComponent(t,
		`component "c" {
  variable "name" {
    type    = string
    default = "x"
  }
}`,
		`scenario "s" {
  component "c" "one" {
    source = "./comp.hcl"
    inputs = {
      name  = "real"
      extra = "stray"
    }
  }
}`)
	if err == nil {
		t.Fatal("expected undeclared-input error at load time")
	}
	if !strings.Contains(err.Error(), "extra") {
		t.Fatalf("error should name the offending input: %v", err)
	}
}

func TestPlanRejectsMissingRequiredComponentInput(t *testing.T) {
	err := loadWithComponent(t,
		`component "c" {
  variable "name" { type = string }
}`,
		`scenario "s" {
  component "c" "one" {
    source = "./comp.hcl"
  }
}`)
	if err == nil {
		t.Fatal("expected missing-required-input error at load time")
	}
	if !strings.Contains(err.Error(), "required variable") || !strings.Contains(err.Error(), "name") {
		t.Fatalf("error should name the missing variable: %v", err)
	}
}

func TestPlanAcceptsValidInputs(t *testing.T) {
	err := loadWithComponent(t,
		`component "c" {
  variable "name" { type = string }
  variable "count" {
    type    = number
    default = 0
  }
}`,
		`scenario "s" {
  component "c" "one" {
    source = "./comp.hcl"
    inputs = {
      name = "real"
    }
  }
}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPlanSkipsNonLiteralInputsGracefully(t *testing.T) {
	// When inputs is a computed expression (not a literal object),
	// plan-time key inspection can't run. The check should skip
	// gracefully; runtime catches any real issues.
	// Note: HCL's parser accepts this shape even though there's no
	// runtime merge() function yet — we just need the expression to
	// not be a literal ObjectConsExpr.
	err := loadWithComponent(t,
		`component "c" {
  variable "name" {
    type    = string
    default = "x"
  }
}`,
		`scenario "s" {
  variable "extra" {
    default = { naem = "typo" }
  }
  component "c" "one" {
    source = "./comp.hcl"
    inputs = var.extra
  }
}`)
	if err != nil {
		t.Fatalf("non-literal inputs should not cause plan-time errors: %v", err)
	}
}
