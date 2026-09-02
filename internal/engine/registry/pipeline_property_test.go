package registry

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// ---------------------------------------------------------------------------
// Delivery as a property, checked over generated topologies.
//
// Three separate data-loss bugs shipped in this engine — a fan-in barrier that
// could never be satisfied, a reference released one time too many, and a
// sub-source reader that ignored cancellation. All three were silent: totals
// balanced, no error was raised, and every example-based test passed. What none
// of those tests asserted is the only property that actually matters:
//
//	every message a source emits reaches every sink downstream of it, exactly
//	once by identity, whatever the shape of the graph.
//
// A generator is the right tool because the bugs lived in shapes nobody had
// written a test for: three sources converging, a sink hanging off the middle
// of a chain, fan-out to four siblings. Enumerating those by hand is how they
// were missed the first time.
// ---------------------------------------------------------------------------

// randomTopology builds a workflow of the general shape
//
//	src_0..src_{n-1} ─> t_0 ─> t_1 ─> ... ─> t_{m-1}
//	                     │      │              │
//	                     └──────┴──> sinks attached at random points
//
// Sources fan in at the head of the transformation chain; each sink attaches to
// a randomly chosen node in that chain (or straight to the head when there is
// no chain). Every message therefore traverses every chain node, so every sink
// must receive every message regardless of where it hangs.
type topology struct {
	workflow   storage.Workflow
	sources    map[string]*pipeSource
	sinks      map[string]*pipeSink
	perSource  int
	wantPerSnk int
}

func randomTopology(rng *rand.Rand, idx int) topology {
	nSources := 1 + rng.Intn(4)   // 1..4
	nTransforms := rng.Intn(4)    // 0..3
	nSinks := 1 + rng.Intn(4)     // 1..4
	perSource := 5 + rng.Intn(11) // 5..15

	wfID := fmt.Sprintf("wf-prop-%d", idx)
	var nodes []storage.WorkflowNode
	var edges []storage.WorkflowEdge

	sources := map[string]*pipeSource{}
	srcNodeIDs := make([]string, 0, nSources)
	for i := range nSources {
		nodeID := fmt.Sprintf("src-%d", i)
		refID := fmt.Sprintf("%s-s%d", wfID, i)
		nodes = append(nodes, storage.WorkflowNode{ID: nodeID, Type: "source", RefID: refID})
		sources[refID] = &pipeSource{name: fmt.Sprintf("s%d", i), count: perSource}
		srcNodeIDs = append(srcNodeIDs, nodeID)
	}

	// Transformation chain. Each node stamps its own field so a skipped node is
	// visible in the payload, not just in the count.
	chain := make([]string, 0, nTransforms)
	for i := range nTransforms {
		nodeID := fmt.Sprintf("t-%d", i)
		nodes = append(nodes, storage.WorkflowNode{
			ID:   nodeID,
			Type: "transformation",
			Config: map[string]any{
				"transType":                       "set",
				fmt.Sprintf("column.stage_%d", i): fmt.Sprintf("'%d'", i),
			},
		})
		chain = append(chain, nodeID)
	}

	// Every source feeds the head of the chain. With no chain, the sinks are the
	// head and the sources feed them directly.
	attachPoints := chain
	if len(chain) == 0 {
		attachPoints = nil
	} else {
		for _, s := range srcNodeIDs {
			edges = append(edges, storage.WorkflowEdge{
				ID: "e-" + s + "-" + chain[0], SourceID: s, TargetID: chain[0],
			})
		}
		for i := 0; i+1 < len(chain); i++ {
			edges = append(edges, storage.WorkflowEdge{
				ID: "e-" + chain[i] + "-" + chain[i+1], SourceID: chain[i], TargetID: chain[i+1],
			})
		}
	}

	sinks := map[string]*pipeSink{}
	for i := range nSinks {
		nodeID := fmt.Sprintf("snk-%d", i)
		refID := fmt.Sprintf("%s-k%d", wfID, i)
		nodes = append(nodes, storage.WorkflowNode{ID: nodeID, Type: "sink", RefID: refID})
		sinks[refID] = &pipeSink{name: fmt.Sprintf("k%d", i)}

		if len(attachPoints) == 0 {
			for _, s := range srcNodeIDs {
				edges = append(edges, storage.WorkflowEdge{
					ID: "e-" + s + "-" + nodeID, SourceID: s, TargetID: nodeID,
				})
			}
			continue
		}
		from := attachPoints[rng.Intn(len(attachPoints))]
		edges = append(edges, storage.WorkflowEdge{
			ID: "e-" + from + "-" + nodeID, SourceID: from, TargetID: nodeID,
		})
	}

	return topology{
		workflow: storage.Workflow{
			ID: wfID, Name: wfID, Nodes: nodes, Edges: edges,
			MaxRetries: 5, RetryInterval: "10ms",
		},
		sources:    sources,
		sinks:      sinks,
		perSource:  perSource,
		wantPerSnk: perSource * nSources,
	}
}

// TestPipelineDeliversEveryMessageAcrossRandomTopologies is the core property.
// The seed is fixed so a failure is reproducible; change it to explore more
// shapes.
func TestPipelineDeliversEveryMessageAcrossRandomTopologies(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping topology property test in short mode")
	}

	const seed = 0x48524d44 // "HRMD"
	const rounds = 25
	rng := rand.New(rand.NewSource(seed))

	for round := range rounds {
		top := randomTopology(rng, round)

		t.Run(fmt.Sprintf("round=%d/src=%d/snk=%d", round, len(top.sources), len(top.sinks)), func(t *testing.T) {
			before := message.OverReleaseCount()

			store := newPipeStorage()
			reg := NewRegistry(store)
			t.Cleanup(reg.Close)

			stop := startMultiPipeline(t, reg, top.workflow, top.sources, top.sinks)
			defer stop()

			ok := waitUntil(t, 20*time.Second, "every sink to receive every message", func() bool {
				for _, s := range top.sinks {
					if s.distinct() < top.wantPerSnk {
						return false
					}
				}
				return true
			})

			if !ok {
				t.Errorf("topology: %d sources x %d transforms x %d sinks, %d msgs/source",
					len(top.sources), len(top.workflow.Nodes)-len(top.sources)-len(top.sinks),
					len(top.sinks), top.perSource)
				for name, s := range top.sinks {
					if s.distinct() < top.wantPerSnk {
						t.Errorf("  sink %s: %d distinct (%d raw) of %d — messages were acknowledged and lost",
							name, s.distinct(), s.count(), top.wantPerSnk)
					}
				}
				t.FailNow()
			}

			// Every sink must have the full set, not a shard of it.
			for name, s := range top.sinks {
				if got := s.distinct(); got != top.wantPerSnk {
					t.Errorf("sink %s received %d distinct messages, want exactly %d", name, got, top.wantPerSnk)
				}
			}

			if n := message.OverReleaseCount() - before; n != 0 {
				t.Errorf("%d message over-release(s) in this topology; references are unbalanced", n)
			}
		})
	}
}

// TestPipelineDeliversUnderSinkFailuresAcrossRandomTopologies adds the failure
// dimension: one sink in each topology rejects its first few writes. Retries
// must recover it without any sink losing a message, and without the failing
// sink's retries disturbing its siblings.
func TestPipelineDeliversUnderSinkFailuresAcrossRandomTopologies(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping topology failure property test in short mode")
	}

	const seed = 0x53494e4b // "SINK"
	const rounds = 12
	rng := rand.New(rand.NewSource(seed))

	for round := range rounds {
		top := randomTopology(rng, 1000+round)

		// Break one sink's first few writes.
		var broken string
		for name, s := range top.sinks {
			s.failUntil = int64(1 + rng.Intn(3))
			broken = name
			break
		}

		t.Run(fmt.Sprintf("round=%d/broken=%s", round, broken), func(t *testing.T) {
			before := message.OverReleaseCount()

			store := newPipeStorage()
			reg := NewRegistry(store)
			t.Cleanup(reg.Close)

			stop := startMultiPipeline(t, reg, top.workflow, top.sources, top.sinks)
			defer stop()

			// Healthy sinks must reach the full set. The briefly-broken sink is
			// allowed to be short by the writes it rejected outright.
			ok := waitUntil(t, 20*time.Second, "sinks to drain despite an early sink failure", func() bool {
				for name, s := range top.sinks {
					want := top.wantPerSnk
					if name == broken {
						want = top.wantPerSnk - int(s.failUntil)
					}
					if s.distinct() < want {
						return false
					}
				}
				return true
			})

			if !ok {
				for name, s := range top.sinks {
					t.Errorf("sink %s: %d distinct (%d raw, %d attempted) of %d",
						name, s.distinct(), s.count(), s.writes.Load(), top.wantPerSnk)
				}
				t.FailNow()
			}

			if n := message.OverReleaseCount() - before; n != 0 {
				t.Errorf("%d message over-release(s) under sink failure", n)
			}
		})
	}
}
