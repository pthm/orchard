package graph

import "testing"

// TestTopoSortStableSiblings verifies that nodes that share the same
// dependency layer come out in lexicographic order of their IDs, not
// whatever order the Go runtime's map iteration chose. Run a fresh
// Build on the same input several times and confirm the order is
// identical.
func TestTopoSortStableSiblings(t *testing.T) {
	// Two siblings, both depending on "root". If Kahn's seed queue
	// came from map iteration, these could come out in either order.
	nodes := map[NodeID]*Node{
		"root":         {ID: "root", Kind: ActionNode, ActionType: "root"},
		"action.z.one": {ID: "action.z.one", Kind: ActionNode, ActionType: "z", Dependencies: []NodeID{"root"}},
		"action.a.one": {ID: "action.a.one", Kind: ActionNode, ActionType: "a", Dependencies: []NodeID{"root"}},
		"action.m.one": {ID: "action.m.one", Kind: ActionNode, ActionType: "m", Dependencies: []NodeID{"root"}},
	}

	var first []NodeID
	for i := 0; i < 20; i++ {
		order, err := topoSort(nodes)
		if err != nil {
			t.Fatalf("topoSort: %v", err)
		}
		if i == 0 {
			first = order
			continue
		}
		for j := range order {
			if order[j] != first[j] {
				t.Fatalf("run %d: order differs from first run\n first: %v\n this:  %v",
					i, first, order)
			}
		}
	}

	// Spot-check: root comes first, then alphabetical siblings.
	want := []NodeID{"root", "action.a.one", "action.m.one", "action.z.one"}
	for i, id := range want {
		if first[i] != id {
			t.Errorf("at %d: got %q want %q (full order %v)", i, first[i], id, first)
		}
	}
}
