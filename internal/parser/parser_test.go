package parser

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testdataPath(name string) string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "testdata", name)
}

func TestLoadScenario_Simple(t *testing.T) {
	ps, err := LoadScenario(testdataPath("simple_scenario.hcl"), nil)
	if err != nil {
		t.Fatalf("LoadScenario failed: %v", err)
	}

	if ps.Scenario.Name != "test_scenario" {
		t.Errorf("expected scenario name %q, got %q", "test_scenario", ps.Scenario.Name)
	}

	if len(ps.Scenario.Variables) != 2 {
		t.Errorf("expected 2 variables, got %d", len(ps.Scenario.Variables))
	}

	if len(ps.Scenario.Actions) != 2 {
		t.Errorf("expected 2 actions, got %d", len(ps.Scenario.Actions))
	}

	if len(ps.Scenario.Outputs) != 1 {
		t.Errorf("expected 1 output, got %d", len(ps.Scenario.Outputs))
	}

	// Check variable resolution.
	if v, ok := ps.Variables["merchant_name"]; !ok {
		t.Error("expected merchant_name variable")
	} else if v.AsString() != "Acme Corp" {
		t.Errorf("expected merchant_name %q, got %q", "Acme Corp", v.AsString())
	}

	// Check action labels.
	a0 := ps.Scenario.Actions[0]
	if a0.Type != "sql" || a0.Name != "create_merchant" {
		t.Errorf("expected action sql.create_merchant, got %s.%s", a0.Type, a0.Name)
	}
}

func TestLoadScenario_VarOverride(t *testing.T) {
	ps, err := LoadScenario(testdataPath("simple_scenario.hcl"), map[string]string{
		"merchant_name": "Override Inc",
	})
	if err != nil {
		t.Fatalf("LoadScenario failed: %v", err)
	}

	if v := ps.Variables["merchant_name"].AsString(); v != "Override Inc" {
		t.Errorf("expected overridden merchant_name %q, got %q", "Override Inc", v)
	}

	// region should still have its default.
	if v := ps.Variables["region"].AsString(); v != "us" {
		t.Errorf("expected region default %q, got %q", "us", v)
	}
}

func TestLoadScenario_WithComponent(t *testing.T) {
	ps, err := LoadScenario(testdataPath("scenario_with_component.hcl"), nil)
	if err != nil {
		t.Fatalf("LoadScenario failed: %v", err)
	}

	if len(ps.Components) != 1 {
		t.Fatalf("expected 1 component, got %d", len(ps.Components))
	}

	comp, ok := ps.Components["merchant.base"]
	if !ok {
		t.Fatal("expected component merchant.base")
	}

	if comp.Def.Name != "merchant" {
		t.Errorf("expected component def name %q, got %q", "merchant", comp.Def.Name)
	}

	if len(comp.Def.Actions) != 1 {
		t.Errorf("expected 1 action in component, got %d", len(comp.Def.Actions))
	}

	if len(comp.Def.Outputs) != 2 {
		t.Errorf("expected 2 outputs in component, got %d", len(comp.Def.Outputs))
	}
}

func TestLoadScenario_RequiredProvidersAndConfig(t *testing.T) {
	ps, err := LoadScenario(testdataPath("simple_scenario.hcl"), nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}

	pg, ok := ps.RequiredProviders["postgres"]
	if !ok {
		t.Fatal("expected postgres in required providers")
	}
	if pg.Source != "builtin/postgres" {
		t.Errorf("postgres source: got %q", pg.Source)
	}

	cfg, ok := ps.ProviderConfigs["postgres"]
	if !ok {
		t.Fatal("expected postgres provider config")
	}
	dsn := cfg["dsn"]
	if dsn.AsString() != "postgres://localhost:5432/test" {
		t.Errorf("postgres dsn: got %q", dsn.AsString())
	}
}

func TestLoadScenario_ProviderConfigEvaluatesVars(t *testing.T) {
	ps, err := LoadScenario(testdataPath("provider_with_var.hcl"), map[string]string{
		"dsn": "postgres://override:5432/db",
	})
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	got := ps.ProviderConfigs["postgres"]["dsn"].AsString()
	if got != "postgres://override:5432/db" {
		t.Errorf("overridden dsn: got %q", got)
	}
}

func TestLoadScenario_ProviderWithoutRequired(t *testing.T) {
	_, err := LoadScenario(testdataPath("provider_undeclared.hcl"), nil)
	if err == nil {
		t.Fatal("expected error for undeclared provider")
	}
	if !strings.Contains(err.Error(), "http") || !strings.Contains(err.Error(), "required_providers") {
		t.Fatalf("error message should mention http and required_providers, got %q", err.Error())
	}
}

func TestLoadScenario_RequiredMissingSource(t *testing.T) {
	_, err := LoadScenario(testdataPath("required_missing_source.hcl"), nil)
	if err == nil {
		t.Fatal("expected error for missing source")
	}
	if !strings.Contains(err.Error(), "source") {
		t.Fatalf("error should mention source, got %q", err.Error())
	}
}

func TestLoadScenario_NoRequiredProviders(t *testing.T) {
	// The with-component testdata has no required_providers block;
	// that must be legal (component-only scenarios exist) and leave
	// RequiredProviders / ProviderConfigs as non-nil empty maps.
	ps, err := LoadScenario(testdataPath("scenario_with_component.hcl"), nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	if ps.RequiredProviders == nil {
		t.Error("RequiredProviders should be empty, not nil")
	}
	if ps.ProviderConfigs == nil {
		t.Error("ProviderConfigs should be empty, not nil")
	}
	if len(ps.RequiredProviders) != 0 {
		t.Errorf("expected 0 required providers, got %d", len(ps.RequiredProviders))
	}
}
