package traversal

import (
	"sync/atomic"
	"time"

	"github.com/gsoultan/hermod"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// SampleInterval is the most often a node's payload sample is refreshed.
//
// A sample is a full ToMap of the message, and every new one is pushed to the
// editor, where it re-fires the previews of everything downstream. Once a
// second is live enough to inspect and cheap at any message rate.
const SampleInterval = time.Second

// Telemetry is a workflow's per-node and per-edge counters, resolved once when
// the router is built so that a message pays only atomic operations for them:
// no map lookup by node ID, no edge key built, no allocation.
//
// It is shared by every traversal of the workflow and safe for concurrent use.
// A nil *Telemetry records nothing.
type Telemetry struct {
	eng *pkgengine.Engine

	// nodes is indexed by NodeIndex.
	nodes []*telemetry.NodeStats
	// edges is indexed by NodeIndex, then by position in Adj[nodeID]: the
	// order handleResults walks the targets in.
	edges [][]*atomic.Uint64

	// watchers counts the clients watching this workflow's status. Payload
	// samples are taken only while it is above zero; nil means never.
	watchers *atomic.Int32
}

// NewTelemetry resolves the counters for every node and edge of a workflow.
// adj must not change afterwards: edge counters are matched to targets by
// position.
func NewTelemetry(eng *pkgengine.Engine, nodeIndex map[string]int, adj map[string][]string, watchers *atomic.Int32) *Telemetry {
	if eng == nil {
		return nil
	}
	tel := &Telemetry{
		eng:      eng,
		nodes:    make([]*telemetry.NodeStats, len(nodeIndex)),
		edges:    make([][]*atomic.Uint64, len(nodeIndex)),
		watchers: watchers,
	}
	for id, idx := range nodeIndex {
		if idx < 0 || idx >= len(tel.nodes) {
			continue
		}
		tel.nodes[idx] = eng.NodeStats(id)
		targets := adj[id]
		if len(targets) == 0 {
			continue
		}
		counters := make([]*atomic.Uint64, len(targets))
		for i, target := range targets {
			counters[i] = eng.EdgeCounter(id, target)
		}
		tel.edges[idx] = counters
	}
	return tel
}

func (tel *Telemetry) node(idx int) *telemetry.NodeStats {
	if tel == nil || idx < 0 || idx >= len(tel.nodes) {
		return nil
	}
	return tel.nodes[idx]
}

// count records a message passing a node that does no timed work (a source).
func (tel *Telemetry) count(idx int) {
	if n := tel.node(idx); n != nil {
		n.Count()
	}
}

// observe records one run of a node.
func (tel *Telemetry) observe(idx int, d time.Duration, failed bool) {
	if n := tel.node(idx); n != nil {
		n.Observe(d, failed)
	}
}

// edge records n messages sent along the pos-th edge out of the node at idx.
func (tel *Telemetry) edge(idx, pos, n int) {
	if tel == nil || idx < 0 || idx >= len(tel.edges) {
		return
	}
	counters := tel.edges[idx]
	if pos < 0 || pos >= len(counters) {
		return
	}
	counters[pos].Add(uint64(n))
}

// sample stores msg as the node's latest payload sample, if anyone is watching
// the workflow and the node's interval has elapsed. A zero now is read from
// the clock only once the watcher check has passed. The watcher check comes
// first: it is one atomic load, and with nobody watching — the common case —
// it is all this costs.
func (tel *Telemetry) sample(idx int, nodeID string, msg hermod.Message, now time.Time) {
	if tel == nil || tel.watchers == nil || msg == nil || tel.watchers.Load() <= 0 {
		return
	}
	n := tel.node(idx)
	if n == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	if !n.ClaimSample(now.UnixNano(), SampleInterval) {
		return
	}
	// ToMap builds an independent copy, which UpdateNodeSample requires.
	tel.eng.UpdateNodeSample(nodeID, msg.ToMap())
}
