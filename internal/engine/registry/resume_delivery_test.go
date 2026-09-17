package registry

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// ---------------------------------------------------------------------------
// Resume runs a different executor from the live pipeline.
//
// A message parked by a wait node, or held at an approval node, comes back
// through runWorkflowNodeFromReplay rather than the traversal. That path writes
// to the sink directly — no writer queue, no retry, no circuit breaker — and
// the live pipeline's guarantees do not come with it. What matters most is the
// last one: the write's error.
// ---------------------------------------------------------------------------

// refusingSink fails every write, like a destination that is down.
type refusingSink struct{}

func (refusingSink) Write(context.Context, hermod.Message) error {
	return errors.New("destination refused the write")
}
func (refusingSink) Ping(context.Context) error { return nil }
func (refusingSink) Close() error               { return nil }

// parkingSink stands in for the dead-letter sink and records what it took.
type parkingSink struct {
	mu  sync.Mutex
	ids []string
}

func (p *parkingSink) Write(_ context.Context, msg hermod.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if msg != nil {
		p.ids = append(p.ids, msg.ID())
	}
	return nil
}
func (p *parkingSink) Ping(context.Context) error { return nil }
func (p *parkingSink) Close() error               { return nil }

func (p *parkingSink) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.ids)
}

// resumeFixture builds the smallest workflow that resumes into a sink: a wait
// node W whose only edge goes to sink node K.
func resumeFixture() (storage.Workflow, map[string]*storage.WorkflowNode, map[string][]string, map[string]int) {
	wf := storage.Workflow{
		ID: "wf-resume",
		Nodes: []storage.WorkflowNode{
			{ID: "W", Type: "wait"},
			{ID: "K", Type: "sink"},
		},
		Edges: []storage.WorkflowEdge{{SourceID: "W", TargetID: "K"}},
	}
	nodeMap := map[string]*storage.WorkflowNode{}
	for i := range wf.Nodes {
		nodeMap[wf.Nodes[i].ID] = &wf.Nodes[i]
	}
	adj := map[string][]string{"W": {"K"}}
	sinkNodeToIndex := map[string]int{"K": 0}
	return wf, nodeMap, adj, sinkNodeToIndex
}

// A resumed message whose sink write fails used to vanish: the error from
// sinks[idx].Write was discarded, so nothing was retried, nothing was parked,
// nothing was logged, and the suspended row was deleted straight afterwards.
// The wait node is what a workflow uses to hold a message until a destination
// is ready, so this is the exact moment the destination is most likely down.
func TestAResumedMessageTheSinkRefusesIsDeadLettered(t *testing.T) {
	wf, nodeMap, adj, sinkNodeToIndex := resumeFixture()

	dlq := &parkingSink{}
	eng := pkgengine.NewEngine(nil, nil, nil)
	eng.SetDeadLetterSink(dlq)

	r := &Registry{}

	m := message.AcquireMessage()
	defer m.Release()
	m.SetID("resumed-1")
	m.SetPayload([]byte(`{"v":1}`))

	r.resumeFromNode("wf-resume", "W", m, eng, wf, nodeMap, adj,
		[]hermod.Sink{refusingSink{}}, sinkNodeToIndex, "")

	if got := dlq.count(); got != 1 {
		t.Errorf("a resumed message that the sink refused was parked %d time(s), want 1; "+
			"the write error is discarded, so the message is gone and the suspended row "+
			"is deleted right after", got)
	}
}

// The success path must stay a success: a working sink takes the message and
// nothing is dead-lettered.
func TestAResumedMessageTheSinkAcceptsIsNotDeadLettered(t *testing.T) {
	wf, nodeMap, adj, sinkNodeToIndex := resumeFixture()

	dlq := &parkingSink{}
	dest := &parkingSink{}
	eng := pkgengine.NewEngine(nil, nil, nil)
	eng.SetDeadLetterSink(dlq)

	r := &Registry{}

	m := message.AcquireMessage()
	defer m.Release()
	m.SetID("resumed-2")
	m.SetPayload([]byte(`{"v":1}`))

	r.resumeFromNode("wf-resume", "W", m, eng, wf, nodeMap, adj,
		[]hermod.Sink{dest}, sinkNodeToIndex, "")

	if got := dest.count(); got != 1 {
		t.Errorf("the resumed message reached the sink %d time(s), want 1", got)
	}
	if got := dlq.count(); got != 0 {
		t.Errorf("a resumed message that was delivered was also dead-lettered %d time(s)", got)
	}
}

// Resume with no live engine (the approval and event-store paths build their
// own sinks and may run with the workflow stopped) must not panic, and must
// still say the message was lost rather than pass over it silently.
func TestResumeWithNoEngineDoesNotPanic(t *testing.T) {
	wf, nodeMap, adj, sinkNodeToIndex := resumeFixture()

	r := &Registry{}

	m := message.AcquireMessage()
	defer m.Release()
	m.SetID("resumed-3")

	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("resuming with no engine panicked: %v", rec)
		}
	}()

	r.resumeFromNode("wf-resume", "W", m, nil, wf, nodeMap, adj,
		[]hermod.Sink{refusingSink{}}, sinkNodeToIndex, "")
}
