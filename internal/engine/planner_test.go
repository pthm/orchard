package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

// planningProvider records the attrs it's asked to plan and fails on
// request, to check ValidateReferences' ActionPlanner hook.
type planningProvider struct {
	planned map[string]map[string]cty.Value
	fail    error
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

func (p *planningProvider) PlanAction(_ context.Context, action string, attrs map[string]cty.Value) (cty.Type, error) {
	if p.planned == nil {
		p.planned = map[string]map[string]cty.Value{}
	}
	p.planned[attrs["table"].AsString()] = attrs
	return cty.DynamicPseudoType, p.fail
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
