package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

// validatingProvider declares a schema with one required attr and
// one optional attr so the test can exercise the structural-check
// branches without writing a lot of boilerplate per test.
type validatingProvider struct{}

func (validatingProvider) Schema() protocol.ProviderSchema {
	return protocol.ProviderSchema{
		Name: "vp", Version: "1", Protocol: protocol.ProtocolVersion,
		Actions: map[string]protocol.ActionSchema{
			"do": {
				Attrs: map[string]protocol.AttrSchema{
					"must":  {Type: "string", Required: true},
					"extra": {Type: "number"},
				},
			},
		},
	}
}
func (validatingProvider) Configure(context.Context, map[string]cty.Value) error { return nil }
func (validatingProvider) Execute(context.Context, string, map[string]cty.Value) (map[string]cty.Value, error) {
	return nil, nil
}
func (validatingProvider) Close() error { return nil }

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	reg := provider.NewRegistry()
	if err := reg.Register("vp", validatingProvider{}); err != nil {
		t.Fatal(err)
	}
	eng, err := New(reg)
	if err != nil {
		t.Fatal(err)
	}
	return eng
}

func graphWithAction(body hcl.Body) *graph.Graph {
	return &graph.Graph{
		Nodes: map[graph.NodeID]*graph.Node{
			"action.do.x": {
				ID:         "action.do.x",
				Kind:       graph.ActionNode,
				ActionType: "do",
				Body:       body,
			},
		},
		Order: []graph.NodeID{"action.do.x"},
	}
}

func TestValidateGraphHappyPath(t *testing.T) {
	eng := newTestEngine(t)
	body := parseBody(t, `must = "value"`)
	if err := eng.ValidateGraph(graphWithAction(body)); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestValidateGraphMissingRequired(t *testing.T) {
	eng := newTestEngine(t)
	body := parseBody(t, `extra = 5`)
	err := eng.ValidateGraph(graphWithAction(body))
	if err == nil || !strings.Contains(err.Error(), "must") {
		t.Fatalf("expected missing-required error, got %v", err)
	}
}

func TestValidateGraphUnknownAttribute(t *testing.T) {
	eng := newTestEngine(t)
	body := parseBody(t, `
must = "ok"
surprise = true
`)
	err := eng.ValidateGraph(graphWithAction(body))
	if err == nil || !strings.Contains(err.Error(), "surprise") {
		t.Fatalf("expected unknown-attr error, got %v", err)
	}
}

func TestValidateGraphUnknownActionType(t *testing.T) {
	eng := newTestEngine(t)
	g := &graph.Graph{
		Nodes: map[graph.NodeID]*graph.Node{
			"action.unknown.x": {
				ID: "action.unknown.x", Kind: graph.ActionNode, ActionType: "unknown",
			},
		},
		Order: []graph.NodeID{"action.unknown.x"},
	}
	err := eng.ValidateGraph(g)
	if err == nil || !strings.Contains(err.Error(), "no provider") {
		t.Fatalf("expected no-provider error, got %v", err)
	}
}

func TestValidateGraphSkipsComponentNodes(t *testing.T) {
	// Component nodes have Body=nil and are validated implicitly
	// through their internal actions. ValidateGraph must not touch
	// them directly.
	eng := newTestEngine(t)
	g := &graph.Graph{
		Nodes: map[graph.NodeID]*graph.Node{
			"component.merchant.base": {
				ID: "component.merchant.base", Kind: graph.ComponentNode,
			},
		},
		Order: []graph.NodeID{"component.merchant.base"},
	}
	if err := eng.ValidateGraph(g); err != nil {
		t.Fatalf("component-only graph should validate cleanly, got %v", err)
	}
}

func TestValidateActionBodyNilWithRequired(t *testing.T) {
	err := validateActionBody(nil, protocol.ActionSchema{
		Attrs: map[string]protocol.AttrSchema{
			"must": {Type: "string", Required: true},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "must") {
		t.Fatalf("expected missing-required, got %v", err)
	}
}

func TestValidateActionBodyNilWithoutRequired(t *testing.T) {
	err := validateActionBody(nil, protocol.ActionSchema{
		Attrs: map[string]protocol.AttrSchema{
			"extra": {Type: "number"},
		},
	})
	if err != nil {
		t.Fatalf("optional-only nil body should validate, got %v", err)
	}
}
