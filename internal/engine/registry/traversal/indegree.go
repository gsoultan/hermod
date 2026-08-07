package traversal

// ReachableInDegree returns, for a traversal that starts at entryNodeID, the
// number of in-edges each node can actually receive during that traversal.
//
// A node fires once ResolvedCount reaches this number, so it has to count only
// the edges that this traversal can deliver or prune. Two kinds of in-edge look
// identical in the raw edge list but behave completely differently:
//
//   - Fan-out inside the traversal (a switch's branches rejoining, a node with
//     several downstream paths that converge). Every such edge is reached, and
//     the ones on untaken branches are pruned, so the barrier resolves. These
//     must be counted — that is what makes a join wait for its branches.
//
//   - Edges from a sibling source node. A message enters the workflow at
//     exactly one source, so the other sources are never visited and their
//     edges are neither resolved nor pruned. Counting them makes the barrier
//     unsatisfiable: the convergence node never fires, and because the engine
//     has already acknowledged the message to its source, the data is dropped
//     with no dead-letter record. These must not be counted.
//
// Restricting the in-degree to the subgraph reachable from the entry node draws
// exactly that distinction, without needing to know which node types are
// "joins".
//
// The result depends only on the topology and the entry node, so callers
// precompute it once per workflow (one map per source node) rather than paying
// a graph walk per message.
func ReachableInDegree(adj map[string][]string, entryNodeID string) map[string]int {
	reachable := map[string]bool{entryNodeID: true}
	queue := []string{entryNodeID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range adj[cur] {
			if !reachable[next] {
				reachable[next] = true
				queue = append(queue, next)
			}
		}
	}

	inDegree := make(map[string]int, len(reachable))
	for _, from := range sortedKeys(adj) {
		if !reachable[from] {
			continue
		}
		for _, to := range adj[from] {
			inDegree[to]++
		}
	}
	return inDegree
}

// ReachableInDegreeByEntry precomputes ReachableInDegree for every entry node,
// which is the form the router consumes: one lookup by source node id per
// message instead of a graph walk.
func ReachableInDegreeByEntry(adj map[string][]string, entryNodeIDs []string) map[string]map[string]int {
	out := make(map[string]map[string]int, len(entryNodeIDs))
	for _, id := range entryNodeIDs {
		out[id] = ReachableInDegree(adj, id)
	}
	return out
}

// sortedKeys keeps the walk order deterministic. The counts do not depend on
// order, but a stable iteration makes the function's behaviour reproducible
// under `-race` and easier to reason about in tests.
func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Insertion sort: these maps are workflow-sized (tens of nodes), and this
	// avoids pulling "sort" into a package on the message path.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
