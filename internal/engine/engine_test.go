package engine

import (
	"context"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/internal/provider"
)

// fakeProvider returns static outputs without executing anything.
type fakeProvider struct {
	outputs map[string]cty.Value
}

func (f *fakeProvider) Execute(_ context.Context, body hcl.Body, evalCtx *hcl.EvalContext) (map[string]cty.Value, error) {
	return f.outputs, nil
}

func TestBuildEvalContext(t *testing.T) {
	vars := map[string]cty.Value{
		"name": cty.StringVal("Acme"),
	}
	resolved := map[string]map[string]cty.Value{
		"action.sql.create_user": {
			"id":    cty.StringVal("42"),
			"email": cty.StringVal("test@example.com"),
		},
	}

	ctx := buildEvalContext(vars, resolved)

	// Check var.name
	varObj := ctx.Variables["var"]
	if varObj.IsNull() {
		t.Fatal("var namespace missing")
	}
	nameVal := varObj.GetAttr("name")
	if nameVal.AsString() != "Acme" {
		t.Errorf("expected var.name = Acme, got %s", nameVal.AsString())
	}

	// Check action.sql.create_user.id
	actionObj := ctx.Variables["action"]
	if actionObj.IsNull() {
		t.Fatal("action namespace missing")
	}
	idVal := actionObj.GetAttr("sql").GetAttr("create_user").GetAttr("id")
	if idVal.AsString() != "42" {
		t.Errorf("expected action.sql.create_user.id = 42, got %s", idVal.AsString())
	}
}

func TestEngine_Execute_Simple(t *testing.T) {
	// Build a minimal graph with no HCL body (using fake provider).
	reg := provider.NewRegistry()
	reg.Register("sql", &fakeProvider{
		outputs: map[string]cty.Value{
			"id":   cty.StringVal("1"),
			"name": cty.StringVal("Acme"),
		},
	})

	eng := New(reg)

	// Manually construct a minimal parsed scenario + graph for testing.
	// This tests the engine's execution loop without needing real HCL.
	g := &graph.Graph{
		Nodes: map[graph.NodeID]*graph.Node{
			"action.sql.create": {
				ID:         "action.sql.create",
				Kind:       graph.ActionNode,
				ActionType: "sql",
			},
		},
		Order: []graph.NodeID{"action.sql.create"},
	}

	// A scenario with no outputs for this simple test.
	ps := &parser.ParsedScenario{
		Scenario: &parser.ScenarioBlock{Name: "test"},
		Variables: map[string]cty.Value{},
	}

	result, err := eng.Execute(context.Background(), ps, g)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(result.Outputs) != 0 {
		t.Errorf("expected 0 scenario outputs, got %d", len(result.Outputs))
	}
}
