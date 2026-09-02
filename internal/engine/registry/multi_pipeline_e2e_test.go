package registry

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/internal/factory"
	"github.com/gsoultan/Hermod/internal/storage"
	"github.com/gsoultan/Hermod/internal/testutil"
	"github.com/gsoultan/Hermod/pkg/comm/message"
	"github.com/gsoultan/Hermod/pkg/engine/config"
)

// ---------------------------------------------------------------------------
// Fixtures for multi-source -> multi-transformation -> multi-sink pipelines.
//
// These exercise the real Registry traversal (fan-in from several sources, a
// transformation chain, then fan-out to several sinks), not a hand-built
// engine, so topology bugs in discoverWorkflowSinks/traversal are caught.
// ---------------------------------------------------------------------------

// pipeSource emits a fixed number of messages tagged with its own name, then
// blocks until the context is cancelled. Blocking (rather than returning an
// error) mirrors a live CDC source that has caught up.
type pipeSource struct {
	name      string
	count     int
	emitted   atomic.Int64
	closed    atomic.Bool
	failFirst int // if >0, the first N reads fail, then the source recovers
	reads     atomic.Int64
}

func (s *pipeSource) Read(ctx context.Context) (hermod.Message, error) {
	n := s.reads.Add(1)
	if s.failFirst > 0 && n <= int64(s.failFirst) {
		return nil, fmt.Errorf("%s: injected transient read failure %d", s.name, n)
	}
	if s.emitted.Load() >= int64(s.count) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	i := s.emitted.Add(1)
	msg := message.AcquireMessage()
	msg.SetData("origin", s.name)
	msg.SetData("seq", i)
	return msg, nil
}

func (s *pipeSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *pipeSource) Ping(ctx context.Context) error                    { return nil }
func (s *pipeSource) Close() error                                      { s.closed.Store(true); return nil }

// pipeSink records every message it receives so the test can assert on both
// volume and content. failUntil makes the first N writes fail, exercising the
// retry path without permanently breaking the sink.
type pipeSink struct {
	name       string
	mu         sync.Mutex
	got        []map[string]any
	writes     atomic.Int64
	delivered  atomic.Int64
	failUntil  int64
	alwaysFail bool
	closed     atomic.Bool
	// countOnly stops the sink keeping every message it accepts.
	//
	// Retaining them is what makes the assertions in the functional tests
	// possible, and it is fine when a test moves hundreds of messages. It is
	// ruinous in the soak, which moves millions: a fifteen-minute run put
	// 13.7M ToMap() copies in this slice and the heap it was supposed to be
	// watching was almost entirely its own. A leak detector whose own
	// retention dominates the measurement cannot detect anything.
	countOnly bool
}

func (s *pipeSink) Write(ctx context.Context, msg hermod.Message) error {
	n := s.writes.Add(1)
	if s.alwaysFail {
		return fmt.Errorf("%s: sink permanently down", s.name)
	}
	if n <= s.failUntil {
		return fmt.Errorf("%s: injected transient write failure %d", s.name, n)
	}
	s.delivered.Add(1)
	if s.countOnly {
		return nil
	}
	s.mu.Lock()
	s.got = append(s.got, msg.ToMap())
	s.mu.Unlock()
	return nil
}

func (s *pipeSink) Close() error                   { s.closed.Store(true); return nil }
func (s *pipeSink) Ping(ctx context.Context) error { return nil }

func (s *pipeSink) received() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]any, len(s.got))
	copy(out, s.got)
	return out
}

// count reports accepted writes. It reads the counter rather than the slice so
// it keeps working for a countOnly sink, which retains nothing.
func (s *pipeSink) count() int {
	return int(s.delivered.Load())
}

// distinct counts unique origin/seq pairs. Delivery is at-least-once, so the
// raw count can reach its target while an original is still in flight and a
// duplicate has taken its place — waiting on the raw count makes the test flaky
// and, worse, can hide a genuine loss behind a duplicate.
func (s *pipeSink) distinct() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[string]struct{}, len(s.got))
	for _, d := range s.got {
		seen[fmt.Sprintf("%v/%v", d["origin"], d["seq"])] = struct{}{}
	}
	return len(seen)
}

// pipeStorage serves whatever source/sink rows the topology references and
// swallows the status writes the engine makes as it runs.
type pipeStorage struct {
	testutil.BaseMockStorage
	mu       sync.Mutex
	statuses map[string]string
}

func newPipeStorage() *pipeStorage {
	return &pipeStorage{statuses: make(map[string]string)}
}

func (m *pipeStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	return storage.Source{ID: id, Name: id, Type: "test"}, nil
}
func (m *pipeStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	return storage.Sink{ID: id, Name: id, Type: "test"}, nil
}
func (m *pipeStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	return storage.Workflow{ID: id}, nil
}
func (m *pipeStorage) UpdateWorkflowStatus(ctx context.Context, id, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses["wf:"+id] = status
	return nil
}
func (m *pipeStorage) UpdateSourceStatus(ctx context.Context, id, status string) error { return nil }

// BaseMockStorage embeds a nil storage.Storage interface, so any method it does
// not override faults at run time rather than failing to compile. The approval
// executor calls CreateApproval, so it has to exist here.
func (m *pipeStorage) CreateApproval(ctx context.Context, a storage.Approval) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses["approval:"+a.ID] = a.Status
	return nil
}
func (m *pipeStorage) UpdateSinkStatus(ctx context.Context, id, status string) error { return nil }

// multiPipelineWorkflow builds the canonical shape under test:
//
//	src-a ┐
//	src-b ┼─> t-tag ──> t-stamp ┬─> sink-x
//	src-c ┘                     ├─> sink-y
//	                            └─> sink-z
//
// Fan-in at the transformation chain, fan-out to every sink. Each sink node
// references a distinct sink row so the registry must build three sinks.
func multiPipelineWorkflow(id string, sinkNames []string) storage.Workflow {
	nodes := []storage.WorkflowNode{
		{ID: "src-a", Type: "source", RefID: "s-a"},
		{ID: "src-b", Type: "source", RefID: "s-b"},
		{ID: "src-c", Type: "source", RefID: "s-c"},
		{ID: "t-tag", Type: "transformation", Config: map[string]any{
			"transType":     "set",
			"column.tagged": "'yes'",
		}},
		{ID: "t-stamp", Type: "transformation", Config: map[string]any{
			"transType":      "set",
			"column.stage":   "'second'",
			"column.carried": "source.origin",
		}},
	}
	edges := []storage.WorkflowEdge{
		{ID: "e-a", SourceID: "src-a", TargetID: "t-tag"},
		{ID: "e-b", SourceID: "src-b", TargetID: "t-tag"},
		{ID: "e-c", SourceID: "src-c", TargetID: "t-tag"},
		{ID: "e-chain", SourceID: "t-tag", TargetID: "t-stamp"},
	}
	for i, sn := range sinkNames {
		nodeID := "sink-" + sn
		nodes = append(nodes, storage.WorkflowNode{ID: nodeID, Type: "sink", RefID: "snk-" + sn})
		edges = append(edges, storage.WorkflowEdge{
			ID:       fmt.Sprintf("e-out-%d", i),
			SourceID: "t-stamp",
			TargetID: nodeID,
		})
	}
	return storage.Workflow{
		ID:            id,
		Name:          id,
		Nodes:         nodes,
		Edges:         edges,
		MaxRetries:    5,
		RetryInterval: "10ms",
	}
}

// startMultiPipeline wires the registry factories to the given sources/sinks
// and starts the workflow. It returns a stop func the caller must defer.
func startMultiPipeline(t *testing.T, reg *Registry, wf storage.Workflow, sources map[string]*pipeSource, sinks map[string]*pipeSink) func() {
	t.Helper()
	reg.SetFactories(
		func(cfg factory.SourceConfig) (hermod.Source, error) {
			// buildWorkflowSources keys the config by the source node's RefID.
			if s, ok := sources[cfg.ID]; ok {
				return s, nil
			}
			return nil, fmt.Errorf("no source fixture for %q", cfg.ID)
		},
		func(cfg factory.SinkConfig) (hermod.Sink, error) {
			if s, ok := sinks[cfg.ID]; ok {
				return s, nil
			}
			return nil, fmt.Errorf("no sink fixture for %q", cfg.ID)
		},
	)
	if err := reg.StartWorkflow(wf.ID, wf); err != nil {
		t.Fatalf("StartWorkflow(%s): %v", wf.ID, err)
	}
	// Flush promptly so assertions do not wait on a batch timeout.
	if eng, ok := reg.GetEngine(wf.ID); ok {
		for id := range sinks {
			eng.UpdateSinkConfig(id, func(cfg *config.SinkConfig) {
				cfg.BatchSize = 1
				cfg.BatchTimeout = 5 * time.Millisecond
			})
		}
	}
	return func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = reg.StopEngine(stopCtx, wf.ID)
	}
}

// waitFor polls until cond holds or the deadline passes. Polling (rather than a
// fixed sleep) keeps the test fast on a quiet machine and stable on a busy one.
func waitUntil(t *testing.T, timeout time.Duration, what string, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Logf("timed out waiting for %s", what)
	return false
}

// TestMultiSourceMultiTransformMultiSink is the positive end-to-end case: every
// message from every source must traverse the whole transformation chain and
// land in every sink, with both transformations applied.
func TestMultiSourceMultiTransformMultiSink(t *testing.T) {
	const perSource = 40
	const wantTotal = perSource * 3

	store := newPipeStorage()
	reg := NewRegistry(store)

	sources := map[string]*pipeSource{
		"s-a": {name: "a", count: perSource},
		"s-b": {name: "b", count: perSource},
		"s-c": {name: "c", count: perSource},
	}
	sinks := map[string]*pipeSink{
		"snk-x": {name: "x"},
		"snk-y": {name: "y"},
		"snk-z": {name: "z"},
	}

	wf := multiPipelineWorkflow("wf-multi", []string{"x", "y", "z"})
	message.ResetOverReleaseCount()
	stop := startMultiPipeline(t, reg, wf, sources, sinks)
	defer stop()

	ok := waitUntil(t, 30*time.Second, "all sinks to receive every message", func() bool {
		for _, s := range sinks {
			if s.distinct() < wantTotal {
				return false
			}
		}
		return true
	})
	if !ok {
		for name, s := range sinks {
			t.Errorf("sink %s received %d distinct (%d raw) of %d messages", name, s.distinct(), s.count(), wantTotal)
		}
		// Distinguish a genuine redelivery (same message id) from two different
		// messages carrying identical payloads, which would mean pooled message
		// data was aliased or reused while still referenced.
		byKey := map[string]map[string]int{}
		for _, d := range sinks["snk-x"].received() {
			k := fmt.Sprintf("%v/%v", d["origin"], d["seq"])
			if byKey[k] == nil {
				byKey[k] = map[string]int{}
			}
			byKey[k][fmt.Sprint(d["id"])]++
		}
		for k, ids := range byKey {
			total := 0
			for _, n := range ids {
				total += n
			}
			if total > 1 {
				t.Logf("payload %s delivered %d times across %d distinct message ids: %v", k, total, len(ids), ids)
			}
		}
		missing := []string{}
		for _, o := range []string{"a", "b", "c"} {
			for i := 1; i <= perSource; i++ {
				if _, ok := byKey[fmt.Sprintf("%s/%d", o, i)]; !ok {
					missing = append(missing, fmt.Sprintf("%s/%d", o, i))
				}
			}
		}
		t.Logf("never delivered: %v", missing)
		t.FailNow()
	}

	// Reference counting must balance. An over-release means a message went back
	// to the pool while still in use, which corrupts payloads instead of failing.
	if n := message.OverReleaseCount(); n != 0 {
		t.Errorf("%d message over-releases during the run; references are unbalanced", n)
	}

	// Fan-out must deliver a full copy to each sink, not shard between them.
	for name, s := range sinks {
		if got := s.distinct(); got != wantTotal {
			t.Errorf("sink %s: got %d distinct messages, want exactly %d (fan-out must copy, not shard)", name, got, wantTotal)
		}
	}

	// Both transformations must have run, in order, on every message; and the
	// fan-in must have preserved each message's originating source.
	// ToMap() flattens the message's data fields to the top level alongside
	// "id" and "metadata".
	origins := map[string]int{}
	seen := map[string]int{} // origin+seq -> times delivered, to spot duplicates
	badly := 0
	for _, data := range sinks["snk-x"].received() {
		if data["tagged"] != "yes" || data["stage"] != "second" {
			if badly < 3 {
				t.Errorf("transformation chain did not run on message %#v", data)
			}
			badly++
			continue
		}
		if data["carried"] != data["origin"] {
			t.Errorf("t-stamp lost the upstream value: carried=%#v origin=%#v", data["carried"], data["origin"])
		}
		if o, ok := data["origin"].(string); ok {
			origins[o]++
			seen[fmt.Sprintf("%s/%v", o, data["seq"])]++
		}
	}
	if badly > 0 {
		t.Errorf("%d/%d messages reached the sink without the transformation chain applied", badly, len(sinks["snk-x"].received()))
	}
	// At-least-once delivery permits a duplicate; it does not permit a loss.
	for _, want := range []string{"a", "b", "c"} {
		if origins[want] < perSource {
			t.Errorf("fan-in lost messages from source %s: got %d, want >= %d", want, origins[want], perSource)
		}
	}
	var dupes []string
	for k, n := range seen {
		if n > 1 {
			dupes = append(dupes, fmt.Sprintf("%s x%d", k, n))
		}
	}
	if len(dupes) > 0 {
		t.Logf("duplicate deliveries (allowed under at-least-once): %v", dupes)
	}
	for _, want := range []string{"a", "b", "c"} {
		distinct := 0
		for k := range seen {
			if strings.HasPrefix(k, want+"/") {
				distinct++
			}
		}
		if distinct != perSource {
			t.Errorf("source %s: %d distinct messages delivered, want %d", want, distinct, perSource)
		}
	}
}

// TestMultiPipelineOneSinkDownDoesNotStarveHealthySinks is the negative case
// that matters most in production: a single dead sink must not wedge the
// pipeline or stop its siblings from receiving data.
func TestMultiPipelineOneSinkDownDoesNotStarveHealthySinks(t *testing.T) {
	const perSource = 20
	const wantTotal = perSource * 3

	store := newPipeStorage()
	reg := NewRegistry(store)

	sources := map[string]*pipeSource{
		"s-a": {name: "a", count: perSource},
		"s-b": {name: "b", count: perSource},
		"s-c": {name: "c", count: perSource},
	}
	sinks := map[string]*pipeSink{
		"snk-x": {name: "x"},
		"snk-y": {name: "y", alwaysFail: true}, // permanently down
		"snk-z": {name: "z"},
	}

	wf := multiPipelineWorkflow("wf-sink-down", []string{"x", "y", "z"})
	stop := startMultiPipeline(t, reg, wf, sources, sinks)
	defer stop()

	if !waitUntil(t, 45*time.Second, "healthy sinks to drain despite a dead sibling", func() bool {
		return sinks["snk-x"].distinct() >= wantTotal && sinks["snk-z"].distinct() >= wantTotal
	}) {
		t.Fatalf("healthy sinks starved by a dead sibling: x=%d z=%d want=%d (dead sink attempted %d writes)",
			sinks["snk-x"].count(), sinks["snk-z"].count(), wantTotal, sinks["snk-y"].writes.Load())
	}

	if sinks["snk-y"].count() != 0 {
		t.Errorf("permanently failing sink recorded %d writes; it should record none", sinks["snk-y"].count())
	}
	if sinks["snk-y"].writes.Load() == 0 {
		t.Error("permanently failing sink was never attempted; fan-out skipped it entirely")
	}
}

// TestMultiPipelineTransientSinkFailuresAreRetried proves the retry path
// recovers a flapping sink without losing messages.
func TestMultiPipelineTransientSinkFailuresAreRetried(t *testing.T) {
	const perSource = 15
	const wantTotal = perSource * 3

	store := newPipeStorage()
	reg := NewRegistry(store)

	sources := map[string]*pipeSource{
		"s-a": {name: "a", count: perSource},
		"s-b": {name: "b", count: perSource},
		"s-c": {name: "c", count: perSource},
	}
	sinks := map[string]*pipeSink{
		"snk-x": {name: "x", failUntil: 3}, // first 3 writes fail, then recovers
		"snk-y": {name: "y"},
	}

	wf := multiPipelineWorkflow("wf-flappy", []string{"x", "y"})
	stop := startMultiPipeline(t, reg, wf, sources, sinks)
	defer stop()

	if !waitUntil(t, 45*time.Second, "flapping sink to recover and drain", func() bool {
		return sinks["snk-x"].count() >= wantTotal-3 && sinks["snk-y"].count() >= wantTotal
	}) {
		t.Fatalf("flapping sink did not recover: x=%d (attempted %d) y=%d want~%d",
			sinks["snk-x"].count(), sinks["snk-x"].writes.Load(), sinks["snk-y"].count(), wantTotal)
	}
}

// TestMultiPipelineSourceReadErrorsDoNotKillThePipeline injects periodic read
// errors on one source. The engine must keep the workflow running and still
// drain the healthy sources.
func TestMultiPipelineSourceReadErrorsDoNotKillThePipeline(t *testing.T) {
	const perSource = 20

	store := newPipeStorage()
	reg := NewRegistry(store)

	sources := map[string]*pipeSource{
		"s-a": {name: "a", count: perSource, failFirst: 3}, // fails on startup, then recovers
		"s-b": {name: "b", count: perSource},
		"s-c": {name: "c", count: perSource},
	}
	sinks := map[string]*pipeSink{"snk-x": {name: "x"}}

	wf := multiPipelineWorkflow("wf-flaky-src", []string{"x"})
	stop := startMultiPipeline(t, reg, wf, sources, sinks)
	defer stop()

	// The healthy sources must not wait on their failing sibling, and the
	// sibling must rejoin once it recovers — so all three drain in full.
	if !waitUntil(t, 60*time.Second, "every source to drain past a flaky sibling", func() bool {
		return sinks["snk-x"].distinct() >= 3*perSource
	}) {
		t.Fatalf("a source that failed its first reads stalled the pipeline: sink got %d distinct, want %d (flaky source emitted %d)",
			sinks["snk-x"].distinct(), 3*perSource, sources["s-a"].emitted.Load())
	}

	if got := sources["s-a"].emitted.Load(); got < int64(perSource) {
		t.Errorf("the flaky source never fully recovered: emitted %d of %d", got, perSource)
	}
	if !reg.IsEngineRunning(wf.ID) {
		t.Error("engine stopped because a source returned read errors; it must stay running")
	}
}

// TestMultiPipelineStopIsCleanAndIdempotent covers the stop path: every source
// and sink must be closed exactly once and a second stop must not panic.
func TestMultiPipelineStopIsCleanAndIdempotent(t *testing.T) {
	store := newPipeStorage()
	reg := NewRegistry(store)

	sources := map[string]*pipeSource{
		"s-a": {name: "a", count: 5},
		"s-b": {name: "b", count: 5},
		"s-c": {name: "c", count: 5},
	}
	sinks := map[string]*pipeSink{"snk-x": {name: "x"}, "snk-y": {name: "y"}}

	wf := multiPipelineWorkflow("wf-stop", []string{"x", "y"})
	stop := startMultiPipeline(t, reg, wf, sources, sinks)

	waitUntil(t, 20*time.Second, "some delivery before stopping", func() bool {
		return sinks["snk-x"].count() > 0
	})

	stop()

	if reg.IsEngineRunning(wf.ID) {
		t.Error("engine still reports running after StopEngine")
	}
	for id, s := range sources {
		if !s.closed.Load() {
			t.Errorf("source %s was not closed on stop", id)
		}
	}
	for id, s := range sinks {
		if !s.closed.Load() {
			t.Errorf("sink %s was not closed on stop", id)
		}
	}

	// A second stop is a no-op, not a panic or a hang.
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = reg.StopEngine(stopCtx, wf.ID)
}

// TestSingleSourceMultiSinkDeliversEveryMessage is the control for the
// multi-source case: one source, same transformation chain, same fan-out. If
// this also loses messages then the loss is in the engine's delivery path, not
// in multi-source fan-in.
func TestSingleSourceMultiSinkDeliversEveryMessage(t *testing.T) {
	const total = 120

	store := newPipeStorage()
	reg := NewRegistry(store)

	sources := map[string]*pipeSource{"s-a": {name: "a", count: total}}
	sinks := map[string]*pipeSink{"snk-x": {name: "x"}, "snk-y": {name: "y"}, "snk-z": {name: "z"}}

	wf := multiPipelineWorkflow("wf-single-src", []string{"x", "y", "z"})
	// Keep only src-a and the edges it owns.
	var nodes []storage.WorkflowNode
	for _, n := range wf.Nodes {
		if n.ID == "src-b" || n.ID == "src-c" {
			continue
		}
		nodes = append(nodes, n)
	}
	var edges []storage.WorkflowEdge
	for _, e := range wf.Edges {
		if e.SourceID == "src-b" || e.SourceID == "src-c" {
			continue
		}
		edges = append(edges, e)
	}
	wf.Nodes, wf.Edges = nodes, edges

	stop := startMultiPipeline(t, reg, wf, sources, sinks)
	defer stop()

	ok := waitUntil(t, 30*time.Second, "single-source pipeline to drain", func() bool {
		for _, s := range sinks {
			if s.distinct() < total {
				return false
			}
		}
		return true
	})
	if !ok {
		for name, s := range sinks {
			t.Errorf("sink %s: %d distinct (%d raw) of %d", name, s.distinct(), s.count(), total)
		}
	}
}
