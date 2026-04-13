package graph

import "fmt"

// topoSort performs a topological sort using Kahn's algorithm.
// Returns an error if the graph contains a cycle.
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
			// dep → node (dep must come before node).
			// node depends on dep, so node's in-degree is incremented
			// when we count edges. But in Kahn's we track incoming edges.
			// The edge is dep → node, so node has an incoming edge from dep.
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

	// Seed queue with zero in-degree nodes.
	var queue []NodeID
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	var order []NodeID
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		order = append(order, current)

		for _, dependent := range dependents[current] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}

	if len(order) != len(nodes) {
		return nil, fmt.Errorf("dependency cycle detected: resolved %d of %d nodes", len(order), len(nodes))
	}

	return order, nil
}
