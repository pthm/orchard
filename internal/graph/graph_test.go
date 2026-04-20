package graph

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pthm/orchard/internal/parser"
)

func testdataPath(name string) string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "parser", "testdata", name)
}

func TestBuild_SimpleScenario(t *testing.T) {
	ps, err := parser.LoadScenario(testdataPath("simple_scenario.hcl"), "", nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}

	g, err := Build(ps)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Should have 2 action nodes.
	if len(g.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(g.Nodes))
	}

	// create_merchant should come before create_product.
	merchantIdx := -1
	productIdx := -1
	for i, id := range g.Order {
		switch id {
		case "action.postgres_query.create_merchant":
			merchantIdx = i
		case "action.postgres_query.create_product":
			productIdx = i
		}
	}

	if merchantIdx == -1 {
		t.Fatal("action.postgres_query.create_merchant not in order")
	}
	if productIdx == -1 {
		t.Fatal("action.postgres_query.create_product not in order")
	}
	if merchantIdx >= productIdx {
		t.Errorf("create_merchant (idx %d) should come before create_product (idx %d)", merchantIdx, productIdx)
	}
}

func TestBuild_WithComponent(t *testing.T) {
	ps, err := parser.LoadScenario(testdataPath("scenario_with_component.hcl"), "", nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}

	g, err := Build(ps)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Should have: component.merchant.base, component.merchant.base.action.postgres_query.create
	if len(g.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d: %v", len(g.Nodes), nodeIDs(g))
	}

	// Internal action should come before component node.
	actionIdx := -1
	compIdx := -1
	for i, id := range g.Order {
		switch id {
		case "component.merchant.base.action.postgres_query.create":
			actionIdx = i
		case "component.merchant.base":
			compIdx = i
		}
	}

	if actionIdx == -1 {
		t.Fatal("internal action not in order")
	}
	if compIdx == -1 {
		t.Fatal("component node not in order")
	}
	if actionIdx >= compIdx {
		t.Errorf("internal action (idx %d) should come before component (idx %d)", actionIdx, compIdx)
	}
}

func nodeIDs(g *Graph) []NodeID {
	ids := make([]NodeID, 0, len(g.Nodes))
	for id := range g.Nodes {
		ids = append(ids, id)
	}
	return ids
}
