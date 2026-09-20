package traversal_test

// A panic below a node leaked a reference on every message it produced.
//
// processNode recovers, so a panic in a node's downstream handling does not
// take the worker with it — that is deliberate and right. But the release of
// the messages runNode returned is the last statement in the function, not a
// deferred one, so the recover skipped it. Every message in that slice kept a
// reference it would never get back: never returned to the pool, holding its
// payload for the life of the process.
//
// It matters most exactly where it is worst. A fan-out returns one message per
// array item, so a panic under a 4,000-item fan-out leaks 4,000 messages at
// once — and this codebase has already paid for refcount mistakes on this
// path, in the other direction, as silent double delivery.

import (
	"context"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/traversal"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// panickingRegistry blows up in BroadcastLog, which handleResults calls when a
// node reports an error. That is the shortest real route to a panic below
// runNode, and it needs no unexported access.
//
// Once, not always: processNode's own recover handler also calls BroadcastLog,
// to report the panic it just caught. A fake that panics every time panics
// again inside the deferred handler, where nothing recovers it, and the
// process dies instead of the bug being visible. Worth knowing on its own —
// the panic path is only as robust as the registry it reports through.
type panickingRegistry struct {
	*mockRegistry
	calls    int
	panicked bool
}

func (p *panickingRegistry) BroadcastLog(workflowID, level, msg, details string) {
	p.calls++
	if p.calls == 1 {
		p.panicked = true
		panic("broadcast exploded")
	}
}

func TestAPanicBelowANodeDoesNotLeakItsMessages(t *testing.T) {
	const produced = 5

	var made []*message.DefaultMessage
	base := &mockRegistry{
		LogSvc: nopLogger{},
		RunWorkflowNodeFn: func(_ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
			// Return several messages the caller owns, alongside an error —
			// which is what sends handleResults into BroadcastLog.
			out := make([]hermod.Message, 0, produced)
			for range produced {
				m := message.AcquireMessage()
				m.SetPayload([]byte(`{"v":1}`))
				made = append(made, m)
				out = append(out, m)
			}
			return out, "", context.Canceled
		},
	}
	reg := &panickingRegistry{mockRegistry: base}

	eng := pkgengine.NewEngine(nil, nil, nil)

	nodeMap := map[string]*storage.WorkflowNode{
		"S": {ID: "S", Type: "source"},
		"N": {ID: "N", Type: "passthrough"},
	}
	adj := map[string][]string{"S": {"N"}}
	inDegree := map[string]int{"N": 1}
	nodeIndex := map[string]int{"S": 0, "N": 1}

	srcMsg := message.AcquireMessage()
	srcMsg.SetPayload([]byte(`{"v":0}`))

	tr := traversal.Acquire(reg, eng, "wf-panic", nodeMap, adj, nodeIndex, nil, nil, inDegree, map[string]int{})
	srcMsg.Retain()
	tr.CurrentMessages[nodeIndex["S"]] = srcMsg
	tr.Traverse(context.Background(), "S")
	traversal.Release(tr)

	if !reg.panicked {
		t.Fatal("precondition: BroadcastLog was never reached, so no panic was raised")
	}
	if len(made) != produced {
		t.Fatalf("precondition: the node produced %d messages, want %d", len(made), produced)
	}

	var leaked int
	for _, m := range made {
		if m.RefCount() > 0 {
			leaked++
		}
	}
	if leaked > 0 {
		t.Errorf("%d of %d messages still hold a reference after a panic below the node; "+
			"they never return to the pool and hold their payload for the life of the process",
			leaked, produced)
	}

	srcMsg.Release()
}

type nopLogger struct{}

func (nopLogger) Debug(string, ...any) {}
func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Warn(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}
