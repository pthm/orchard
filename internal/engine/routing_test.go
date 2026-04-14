package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

// tagProvider satisfies Provider with a single action that echoes
// the tag the provider was built with — a quick way to prove which
// provider the engine routed to when two providers share an action
// name.
type tagProvider struct{ tag string }

func (p tagProvider) Schema() protocol.ProviderSchema {
	return protocol.ProviderSchema{
		Name: p.tag, Version: "1", Protocol: protocol.ProtocolVersion,
		Actions: map[string]protocol.ActionSchema{
			"sql": {Attrs: map[string]protocol.AttrSchema{}},
		},
	}
}
func (p tagProvider) Configure(context.Context, map[string]cty.Value) error { return nil }
func (p tagProvider) Execute(context.Context, string, map[string]cty.Value) (map[string]cty.Value, error) {
	return map[string]cty.Value{"picked": cty.StringVal(p.tag)}, nil
}
func (p tagProvider) Close() error { return nil }

func makeEngine(t *testing.T, providers map[string]provider.Provider) *Engine {
	t.Helper()
	reg := provider.NewRegistry()
	// Register in sorted alias order so tests are reproducible.
	for _, name := range sortedKeys(providers) {
		if err := reg.Register(name, providers[name]); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	eng, err := New(reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return eng
}

func sortedKeys(m map[string]provider.Provider) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Deterministic order keeps test logs readable.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func singleActionGraph(actionType, providerAlias string) (*graph.Graph, *parser.ParsedScenario) {
	g := &graph.Graph{
		Nodes: map[graph.NodeID]*graph.Node{
			"action.sql.x": {
				ID:            "action.sql.x",
				Kind:          graph.ActionNode,
				ActionType:    actionType,
				ProviderAlias: providerAlias,
			},
		},
		Order: []graph.NodeID{"action.sql.x"},
	}
	ps := &parser.ParsedScenario{
		Scenario:  &parser.ScenarioBlock{Name: "t"},
		Variables: map[string]cty.Value{},
	}
	return g, ps
}

func TestRoutingPicksSingleCandidate(t *testing.T) {
	eng := makeEngine(t, map[string]provider.Provider{
		"only": tagProvider{tag: "only"},
	})
	g, ps := singleActionGraph("sql", "")
	_, err := eng.Execute(context.Background(), ps, g)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

func TestRoutingAmbiguousWithoutAliasErrors(t *testing.T) {
	eng := makeEngine(t, map[string]provider.Provider{
		"primary":   tagProvider{tag: "primary"},
		"secondary": tagProvider{tag: "secondary"},
	})
	g, ps := singleActionGraph("sql", "")
	_, err := eng.Execute(context.Background(), ps, g)
	if err == nil {
		t.Fatal("expected ambiguous-routing error")
	}
	if !strings.Contains(err.Error(), "multiple providers") {
		t.Errorf("error should mention multiple providers: %v", err)
	}
	if !strings.Contains(err.Error(), "primary") || !strings.Contains(err.Error(), "secondary") {
		t.Errorf("error should list candidate aliases: %v", err)
	}
}

func TestRoutingAmbiguousWithAliasPicks(t *testing.T) {
	eng := makeEngine(t, map[string]provider.Provider{
		"primary":   tagProvider{tag: "primary"},
		"secondary": tagProvider{tag: "secondary"},
	})
	g, ps := singleActionGraph("sql", "secondary")
	_, err := eng.Execute(context.Background(), ps, g)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

func TestRoutingAliasDoesNotExist(t *testing.T) {
	eng := makeEngine(t, map[string]provider.Provider{
		"primary": tagProvider{tag: "primary"},
	})
	g, ps := singleActionGraph("sql", "ghost")
	_, err := eng.Execute(context.Background(), ps, g)
	if err == nil {
		t.Fatal("expected unknown-alias error")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the bad alias: %v", err)
	}
}
