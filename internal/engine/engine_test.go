package engine

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

// fakeProvider returns static outputs without executing anything.
// It satisfies provider.Provider and is named "fake" with a single
// action "sql" so tests can drive the engine without a real DB.
type fakeProvider struct {
	outputs map[string]cty.Value
}

func (f *fakeProvider) Schema() protocol.ProviderSchema {
	return protocol.ProviderSchema{
		Name: "fake", Version: "1", Protocol: protocol.ProtocolVersion,
		Actions: map[string]protocol.ActionSchema{
			"sql": {Attrs: map[string]protocol.AttrSchema{}},
		},
	}
}

func (f *fakeProvider) Configure(context.Context, map[string]cty.Value) error { return nil }

func (f *fakeProvider) Execute(_ context.Context, _ string, _ map[string]cty.Value) (map[string]cty.Value, error) {
	return f.outputs, nil
}

func (f *fakeProvider) Close() error { return nil }

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

	varObj := ctx.Variables["var"]
	if varObj.IsNull() {
		t.Fatal("var namespace missing")
	}
	if got := varObj.GetAttr("name").AsString(); got != "Acme" {
		t.Errorf("var.name: got %q want Acme", got)
	}

	actionObj := ctx.Variables["action"]
	if actionObj.IsNull() {
		t.Fatal("action namespace missing")
	}
	idVal := actionObj.GetAttr("sql").GetAttr("create_user").GetAttr("id")
	if idVal.AsString() != "42" {
		t.Errorf("action.sql.create_user.id: got %q want 42", idVal.AsString())
	}
}

func TestEngineExecuteSimple(t *testing.T) {
	reg := provider.NewRegistry()
	if err := reg.Register("fake", &fakeProvider{
		outputs: map[string]cty.Value{
			"id":   cty.StringVal("1"),
			"name": cty.StringVal("Acme"),
		},
	}); err != nil {
		t.Fatal(err)
	}

	eng, err := New(reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	g := &graph.Graph{
		Nodes: map[graph.NodeID]*graph.Node{
			"action.sql.create": {
				ID:         "action.sql.create",
				Kind:       graph.ActionNode,
				ActionType: "sql",
				// Body nil is fine — fakeProvider's schema declares no
				// attributes, so evalAttrs has nothing to evaluate.
			},
		},
		Order: []graph.NodeID{"action.sql.create"},
	}

	ps := &parser.ParsedScenario{
		Scenario:  &parser.ScenarioBlock{Name: "test"},
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

func TestEngineAcceptsOverlappingActionNames(t *testing.T) {
	// Two providers may expose the same action name — the engine
	// resolves the ambiguity per-action via the scenario's
	// `provider = "<alias>"` attribute. Construction itself must
	// not fail.
	reg := provider.NewRegistry()
	_ = reg.Register("one", &dupProvider{name: "one"})
	_ = reg.Register("two", &dupProvider{name: "two"})

	if _, err := New(reg); err != nil {
		t.Fatalf("overlapping action names should construct cleanly, got %v", err)
	}
}

func TestEngineExecuteUnknownActionType(t *testing.T) {
	reg := provider.NewRegistry()
	_ = reg.Register("fake", &fakeProvider{outputs: map[string]cty.Value{}})

	eng, err := New(reg)
	if err != nil {
		t.Fatal(err)
	}

	g := &graph.Graph{
		Nodes: map[graph.NodeID]*graph.Node{
			"action.unknown.x": {
				ID:         "action.unknown.x",
				Kind:       graph.ActionNode,
				ActionType: "unknown",
			},
		},
		Order: []graph.NodeID{"action.unknown.x"},
	}
	ps := &parser.ParsedScenario{
		Scenario:  &parser.ScenarioBlock{Name: "test"},
		Variables: map[string]cty.Value{},
	}

	_, err = eng.Execute(context.Background(), ps, g)
	if err == nil {
		t.Fatal("expected error for unknown action type")
	}
}

// dupProvider exposes an action named "sql" so two of them collide
// when registered in the same registry. Used by the duplicate-action
// test above.
type dupProvider struct{ name string }

func (d *dupProvider) Schema() protocol.ProviderSchema {
	return protocol.ProviderSchema{
		Name: d.name, Version: "1", Protocol: protocol.ProtocolVersion,
		Actions: map[string]protocol.ActionSchema{
			"sql": {Attrs: map[string]protocol.AttrSchema{}},
		},
	}
}
func (d *dupProvider) Configure(context.Context, map[string]cty.Value) error { return nil }
func (d *dupProvider) Execute(context.Context, string, map[string]cty.Value) (map[string]cty.Value, error) {
	return nil, nil
}
func (d *dupProvider) Close() error { return nil }
