package engine

import (
	"context"
	"errors"
	"fmt"
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

// planningProvider records the attrs it's asked to plan and fails on
// request, to check ValidateReferences' ActionPlanner hook.
type planningProvider struct {
	planned map[string]map[string]cty.Value
	fail    error
	// rowType is returned as every action's output type.
	rowType cty.Type
	// hints are returned for every action.
	hints []string
}

func (p *planningProvider) Schema() protocol.ProviderSchema {
	return protocol.ProviderSchema{
		Name: "pp", Version: "1", Protocol: protocol.ProtocolVersion,
		Actions: map[string]protocol.ActionSchema{
			"pp_make": {
				Attrs:   map[string]protocol.AttrSchema{"table": {Type: "string", Required: true}, "values": {Type: "any", Required: true}},
				Outputs: &protocol.AttrSchema{Type: "any"},
			},
		},
	}
}
func (p *planningProvider) Configure(context.Context, map[string]cty.Value) error { return nil }
func (p *planningProvider) Execute(context.Context, string, map[string]cty.Value) (map[string]cty.Value, error) {
	return nil, nil
}
func (p *planningProvider) Close() error { return nil }

func (p *planningProvider) PlanAction(_ context.Context, action string, attrs map[string]cty.Value) (provider.Plan, error) {
	if p.planned == nil {
		p.planned = map[string]map[string]cty.Value{}
	}
	p.planned[attrs["table"].AsString()] = attrs
	if p.fail != nil {
		return provider.Plan{}, p.fail
	}
	return provider.Plan{Outputs: p.rowType, Hints: p.hints}, nil
}

var _ provider.ActionPlanner = (*planningProvider)(nil)

const plannerScenario = `
scenario "s" {
  required_providers {
    pp = { source = "builtin/pp" }
  }
  variable "name" { default = "Acme" }

  action "pp_make" "merchant" {
    table  = "merchants"
    values = { name = var.name }
  }

  action "pp_make" "product" {
    table  = "products"
    values = { merchant_id = action.pp_make.merchant.id, name = "Widget" }
  }
}
`

func TestValidateReferencesCallsActionPlanner(t *testing.T) {
	p := &planningProvider{}
	if err := validateReferencesFromHCL(t, plannerScenario, map[string]provider.Provider{"pp": p}); err != nil {
		t.Fatalf("ValidateReferences: %v", err)
	}

	merchant := p.planned["merchants"]["values"]
	if !merchant.IsKnown() || !merchant.GetAttr("name").RawEquals(cty.StringVal("Acme")) {
		t.Errorf("merchant values should be fully known: %#v", merchant)
	}
	product := p.planned["products"]["values"]
	if product.GetAttr("merchant_id").IsKnown() {
		t.Errorf("a reference to another action's output should be unknown at plan time: %#v", product)
	}
	if !product.GetAttr("name").RawEquals(cty.StringVal("Widget")) {
		t.Errorf("literal values should be known: %#v", product)
	}
}

func TestValidateReferencesReportsPlannerErrors(t *testing.T) {
	p := &planningProvider{fail: errors.New(`table "merchants": missing required column "region"`)}
	err := validateReferencesFromHCL(t, plannerScenario, map[string]provider.Provider{"pp": p})
	if err == nil || !strings.Contains(err.Error(), "action.pp_make.merchant") ||
		!strings.Contains(err.Error(), `missing required column "region"`) {
		t.Fatalf("expected the planner's error with the action id, got %v", err)
	}
}

func TestPlannerOutputTypesCheckDownstreamReferences(t *testing.T) {
	row := cty.Object(map[string]cty.Type{"id": cty.Number, "name": cty.String})
	src := strings.Replace(plannerScenario, "action.pp_make.merchant.id", "action.pp_make.merchant.idd", 1)

	err := validateReferencesFromHCL(t, src, map[string]provider.Provider{"pp": &planningProvider{rowType: row}})
	if err == nil || !strings.Contains(err.Error(), "action.pp_make.product") || !strings.Contains(err.Error(), "idd") {
		t.Fatalf("a typo against a planner-reported type should fail, got %v", err)
	}

	// With no type reported, the same reference passes (outputs are any).
	if err := validateReferencesFromHCL(t, src, map[string]provider.Provider{"pp": &planningProvider{rowType: cty.DynamicPseudoType}}); err != nil {
		t.Fatalf("dynamic outputs shouldn't be checked: %v", err)
	}
}

func TestPlannerChecksActionsInsideComponents(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "merchant.hcl"), `
component "merchant" {
  variable "name" { type = string }
  action "pp_make" "row" {
    table  = "merchants"
    values = { name = var.name }
  }
  output "id" { value = action.pp_make.row.idd }
}
`)
	writeTestFile(t, filepath.Join(dir, "scenario.hcl"), `
scenario "s" {
  required_providers {
    pp = { source = "builtin/pp" }
  }
  component "merchant" "acme" {
    source = "./merchant.hcl"
    inputs = { name = "Acme" }
  }
}
`)
	p := &planningProvider{rowType: cty.Object(map[string]cty.Type{"id": cty.Number})}
	err := validateFile(t, filepath.Join(dir, "scenario.hcl"), map[string]provider.Provider{"pp": p})
	if _, ok := p.planned["merchants"]; !ok {
		t.Fatalf("the component's action should have been planned (err: %v)", err)
	}
	if !p.planned["merchants"]["values"].GetAttr("name").Type().Equals(cty.String) {
		t.Errorf("component vars should be typed unknowns: %#v", p.planned["merchants"]["values"])
	}
	if err == nil || !strings.Contains(err.Error(), "idd") {
		t.Fatalf("a typo in a component output against a planner type should fail, got %v", err)
	}
}

func TestPlannerWithoutSchemaIsUnchecked(t *testing.T) {
	p := &planningProvider{fail: fmt.Errorf("pp: %w", provider.ErrNoSchema)}
	eng, err := validateWithEngine(t, plannerScenario, map[string]provider.Provider{"pp": p})
	if err != nil {
		t.Fatalf("ErrNoSchema should not fail validation: %v", err)
	}
	if got := eng.UncheckedProviders(); len(got) != 1 || got[0] != "pp" {
		t.Fatalf("UncheckedProviders: %v", got)
	}
}

// validateWithEngine is validateReferencesFromHCL, also returning the
// engine so tests can inspect it.
func validateWithEngine(t *testing.T, src string, providers map[string]provider.Provider) (*Engine, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scenario.hcl")
	writeTestFile(t, path, src)
	return validateFileEngine(t, path, providers)
}

func validateFile(t *testing.T, path string, providers map[string]provider.Provider) error {
	t.Helper()
	_, err := validateFileEngine(t, path, providers)
	return err
}

func validateFileEngine(t *testing.T, path string, providers map[string]provider.Provider) (*Engine, error) {
	t.Helper()
	ps, err := parser.LoadScenario(path, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	g, err := graph.Build(ps)
	if err != nil {
		t.Fatal(err)
	}
	reg := provider.NewRegistry()
	for name, p := range providers {
		if err := reg.Register(name, p); err != nil {
			t.Fatal(err)
		}
	}
	eng, err := New(reg)
	if err != nil {
		t.Fatal(err)
	}
	err = eng.ValidateReferences(context.Background(), ps, g)
	return eng, err
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
