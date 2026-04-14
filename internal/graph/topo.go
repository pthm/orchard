package graph

import (
	"fmt"
	"sort"
)

// topoSort performs a topological sort using Kahn's algorithm.
// Returns an error if the graph contains a cycle.
//
// When multiple nodes are ready at the same time (their in-degree
// drops to zero), they are emitted in lexicographic order of their
// node IDs. This keeps scenario execution order reproducible run
// to run — the data dependencies alone do not fully determine
// sibling order, and map iteration in Go is deliberately randomised.
func topoSort(nodes map[NodeID]*Node) ([]NodeID, error) {
	// Compute in-degree for each node.
	inDegree := make(map[NodeID]int)
	for id := range nodes {
		inDegree[id] = 0
	}
	for _, node := range nodes {
		for _, dep := range node.Dependencies {
			if _, exists := nodes[dep]; !exists {
				return nil, fmt.Errorf("unknown dependency %q referenced by %q", dep, node.ID)
			}
		}
	}

	// Build adjacency list (forward edges: dep → dependents).
	dependents := make(map[NodeID][]NodeID)
	for _, node := range nodes {
		for _, dep := range node.Dependencies {
			dependents[dep] = append(dependents[dep], node.ID)
			inDegree[node.ID]++
		}
	}

	// Seed the ready set with every zero-in-degree node, sorted.
	// The sort is what gives us deterministic sibling ordering.
	ready := make([]NodeID, 0)
	for id, deg := range inDegree {
		if deg == 0 {
			ready = append(ready, id)
		}
	}
	sortNodeIDs(ready)

	var order []NodeID
	for len(ready) > 0 {
		current := ready[0]
		ready = ready[1:]
		order = append(order, current)

		// Collect newly-ready dependents, sort them, then prepend so
		// all siblings discovered this tick come out before the next
		// layer's siblings. Appending + resorting the whole slice
		// would also work; this form keeps the established order of
		// already-queued nodes stable.
		var newly []NodeID
		for _, dependent := range dependents[current] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				newly = append(newly, dependent)
			}
		}
		if len(newly) > 0 {
			sortNodeIDs(newly)
			ready = append(ready, newly...)
		}
	}

	if len(order) != len(nodes) {
		return nil, fmt.Errorf("dependency cycle detected: resolved %d of %d nodes", len(order), len(nodes))
	}

	return order, nil
}

func sortNodeIDs(ids []NodeID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}
