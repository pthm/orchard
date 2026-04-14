package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

// typedOutputProvider exposes a "fetch" action with a fully
// typed output schema. Used to drive plan-time type checking
// without standing up a real provider.
type typedOutputProvider struct{}

func (typedOutputProvider) Schema() protocol.ProviderSchema {
	return protocol.ProviderSchema{
		Name: "tp", Version: "1", Protocol: protocol.ProtocolVersion,
		Actions: map[string]protocol.ActionSchema{
			"fetch": {
				Attrs: map[string]protocol.AttrSchema{
					"url": {Type: "string", Required: true},
				},
				Outputs: &protocol.AttrSchema{
					Type: "object({status=number, body=string, headers=map(string)})",
				},
			},
		},
	}
}
func (typedOutputProvider) Configure(context.Context, map[string]cty.Value) error { return nil }
func (typedOutputProvider) Execute(context.Context, string, map[string]cty.Value) (map[string]cty.Value, error) {
	return nil, nil
}
func (typedOutputProvider) Close() error { return nil }

// dynOutputProvider has `output { type = any }` — the plan-time
// type-check can't catch typos on its references. Used to verify
// that the dynamic-output fall-through works (no false positives,
// no catch of typos either).
type dynOutputProvider struct{}

func (dynOutputProvider) Schema() protocol.ProviderSchema {
	return protocol.ProviderSchema{
		Name: "dp", Version: "1", Protocol: protocol.ProtocolVersion,
		Actions: map[string]protocol.ActionSchema{
			"query": {
				Attrs: map[string]protocol.AttrSchema{
					"q": {Type: "string", Required: true},
				},
				Outputs: &protocol.AttrSchema{Type: "any"},
			},
		},
	}
}
func (dynOutputProvider) Configure(context.Context, map[string]cty.Value) error { return nil }
func (dynOutputProvider) Execute(context.Context, string, map[string]cty.Value) (map[string]cty.Value, error) {
	return nil, nil
}
func (dynOutputProvider) Close() error { return nil }

// validateReferencesFromHCL parses an inline scenario, registers
// the given providers, and runs plan-time validation. Returns the
// first error the plan catches, or nil if validation passes.
func validateReferencesFromHCL(t *testing.T, src string, providers map[string]provider.Provider) error {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.hcl")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
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

func TestValidateRefsTypoOnTypedOutput(t *testing.T) {
	err := validateReferencesFromHCL(t, `
scenario "s" {
  required_providers {
    tp = { source = "builtin/tp" }
  }
  action "fetch" "one" {
    url = "/x"
  }
  output "bad" {
    value = action.fetch.one.bdy
  }
}`, map[string]provider.Provider{"tp": typedOutputProvider{}})
	if err == nil {
		t.Fatal("expected typo error")
	}
	if !strings.Contains(err.Error(), `"bdy"`) {
		t.Errorf("error should name the offending attr: %v", err)
	}
	if !strings.Contains(err.Error(), "Unsupported attribute") {
		t.Errorf("error should be the HCL unsupported-attribute diagnostic: %v", err)
	}
}

func TestValidateRefsValidOutputReference(t *testing.T) {
	err := validateReferencesFromHCL(t, `
scenario "s" {
  required_providers {
    tp = { source = "builtin/tp" }
  }
  action "fetch" "one" {
    url = "/x"
  }
  output "ok_status" { value = action.fetch.one.status }
  output "ok_body"   { value = action.fetch.one.body }
}`, map[string]provider.Provider{"tp": typedOutputProvider{}})
	if err != nil {
		t.Fatalf("valid references should pass: %v", err)
	}
}

func TestValidateRefsDynamicOutputTyposAllowed(t *testing.T) {
	// Documented limitation: dynamic-output actions can't catch
	// typos at plan time. This test pins the behaviour so a future
	// change that tightens it is a deliberate choice, not an
	// accident.
	err := validateReferencesFromHCL(t, `
scenario "s" {
  required_providers {
    dp = { source = "builtin/dp" }
  }
  action "query" "one" {
    q = "SELECT 1"
  }
  output "anything" {
    value = action.query.one.not_a_real_field
  }
}`, map[string]provider.Provider{"dp": dynOutputProvider{}})
	if err != nil {
		t.Errorf("dynamic-output typos should not be caught at plan time: %v", err)
	}
}

func TestValidateRefsNestedAttributeOnPrimitive(t *testing.T) {
	// headers is map(string). .Foo returns string. .bar on a string
	// is invalid — HCL catches this through our typed context.
	err := validateReferencesFromHCL(t, `
scenario "s" {
  required_providers {
    tp = { source = "builtin/tp" }
  }
  action "fetch" "one" {
    url = "/x"
  }
  output "bad" {
    value = action.fetch.one.headers.Foo.bar
  }
}`, map[string]provider.Provider{"tp": typedOutputProvider{}})
	if err == nil {
		t.Fatal("expected error accessing attribute on string")
	}
	if !strings.Contains(err.Error(), "string") {
		t.Errorf("error should mention the string type: %v", err)
	}
}

func TestValidateRefsTypoInActionBodyAttribute(t *testing.T) {
	// Chained actions: second action references the first's output
	// with a typo. Caught here rather than at runtime.
	err := validateReferencesFromHCL(t, `
scenario "s" {
  required_providers {
    tp = { source = "builtin/tp" }
  }
  action "fetch" "first" {
    url = "/a"
  }
  action "fetch" "second" {
    url = "/b/${action.fetch.first.bdy}"
  }
}`, map[string]provider.Provider{"tp": typedOutputProvider{}})
	if err == nil {
		t.Fatal("expected typo error on action body attribute")
	}
	if !strings.Contains(err.Error(), "action.fetch.second.url") {
		t.Errorf("error should locate the offending attribute: %v", err)
	}
}

func TestValidateRefsUnknownVariable(t *testing.T) {
	// A scenario with at least one declared variable so var.* is
	// populated in the typed context. Referencing a name that
	// isn't declared then errors via HCL's "unsupported attribute"
	// rather than "there is no variable named var".
	err := validateReferencesFromHCL(t, `
scenario "s" {
  required_providers {
    tp = { source = "builtin/tp" }
  }
  variable "real" {
    default = "hello"
  }
  action "fetch" "one" {
    url = var.ghost
  }
}`, map[string]provider.Provider{"tp": typedOutputProvider{}})
	if err == nil {
		t.Fatal("expected unknown-variable error")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the unknown variable: %v", err)
	}
}

func TestValidateRefsVariableResolved(t *testing.T) {
	err := validateReferencesFromHCL(t, `
scenario "s" {
  required_providers {
    tp = { source = "builtin/tp" }
  }
  variable "path" {
    default = "/things"
  }
  action "fetch" "one" {
    url = var.path
  }
}`, map[string]provider.Provider{"tp": typedOutputProvider{}})
	if err != nil {
		t.Fatalf("variable reference should pass: %v", err)
	}
}
