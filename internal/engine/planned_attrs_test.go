package engine

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

// defaultsProvider has one action with an optional, defaulted
// attribute and no ActionPlanner, so PlannedAttrs must evaluate it
// on its own.
type defaultsProvider struct{}

func (defaultsProvider) Schema() protocol.ProviderSchema {
	return protocol.ProviderSchema{
		Name: "dp", Version: "1", Protocol: protocol.ProtocolVersion,
		Actions: map[string]protocol.ActionSchema{
			"dp_make": {Attrs: map[string]protocol.AttrSchema{
				"name":   {Type: "string", Required: true},
				"region": {Type: "string", Default: json.RawMessage(`"us"`)},
			}},
		},
	}
}
func (defaultsProvider) Configure(context.Context, map[string]cty.Value) error { return nil }
func (defaultsProvider) Execute(context.Context, string, map[string]cty.Value) (map[string]cty.Value, error) {
	return nil, nil
}
func (defaultsProvider) Close() error { return nil }

func TestPlannedAttrs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scenario.hcl")
	writeTestFile(t, path, `
scenario "s" {
  required_providers {
    dp = { source = "builtin/dp" }
  }
  variable "name" { default = "Acme" }

  action "dp_make" "merchant" {
    name = var.name
  }
  action "dp_make" "product" {
    count = 2
    name  = "${action.dp_make.merchant.id}-${count.index}"
  }
  action "dp_make" "tag" {
    count  = 2
    name   = "tag-${count.index}"
    region = "eu"
  }
}
`)
	ps, err := parser.LoadScenario(path, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	g, err := graph.Build(ps)
	if err != nil {
		t.Fatal(err)
	}
	reg := provider.NewRegistry()
	if err := reg.Register("dp", defaultsProvider{}); err != nil {
		t.Fatal(err)
	}
	eng, err := New(reg)
	if err != nil {
		t.Fatal(err)
	}

	// Off by default: nothing is kept.
	if err := eng.ValidateReferences(context.Background(), ps, g); err != nil {
		t.Fatal(err)
	}
	if got := eng.PlannedAttrs("action.dp_make.merchant"); got != nil {
		t.Fatalf("PlannedAttrs without the option: %#v", got)
	}

	eng.SetKeepPlannedAttrs(true)
	if err := eng.ValidateReferences(context.Background(), ps, g); err != nil {
		t.Fatal(err)
	}
	merchant := eng.PlannedAttrs("action.dp_make.merchant")
	if !merchant["name"].RawEquals(cty.StringVal("Acme")) {
		t.Errorf("var-derived name: %#v", merchant["name"])
	}
	if !merchant["region"].RawEquals(cty.StringVal("us")) {
		t.Errorf("omitted optional attribute should take the schema default: %#v", merchant["region"])
	}
	if v := eng.PlannedAttrs("action.dp_make.product[0]")["name"]; v.IsKnown() {
		t.Errorf("an action output reference should be unknown: %#v", v)
	}
	for i, want := range []string{"tag-0", "tag-1"} {
		id := graph.NodeID("action.dp_make.tag[" + string(rune('0'+i)) + "]")
		if v := eng.PlannedAttrs(id)["name"]; !v.RawEquals(cty.StringVal(want)) {
			t.Errorf("%s name: %#v, want %q", id, v, want)
		}
	}
}
