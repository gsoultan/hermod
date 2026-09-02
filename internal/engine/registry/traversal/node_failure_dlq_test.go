package traversal_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/internal/engine/registry/traversal"
	"github.com/gsoultan/Hermod/internal/storage"
	"github.com/gsoultan/Hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/Hermod/pkg/engine"
)

// ---------------------------------------------------------------------------
// A node that fails must not take the message with it.
//
// The dead-letter sink caught validation failures and sink write failures. A
// node failing inside the workflow — a transformation that cannot parse, a
// condition whose expression is wrong, a wait that cannot record itself — was
// logged as "Node %s failed" and the message was released.
//
// So a workflow with a dead-letter sink configured still lost those messages,
// and the log line looked enough like handling that nobody would go looking.
// ---------------------------------------------------------------------------

// capturingSink records what the dead-letter sink is asked to write.
type capturingSink struct {
	hermod.Sink
	mu   sync.Mutex
	msgs []string
	errs []string
}

func (c *capturingSink) Write(_ context.Context, msg hermod.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if msg != nil {
		c.msgs = append(c.msgs, msg.ID())
		c.errs = append(c.errs, msg.Metadata()["_hermod_last_error"])
	}
	return nil
}

func (c *capturingSink) Close() error { return nil }

func (c *capturingSink) written() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.msgs...)
}

func (c *capturingSink) lastError() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.errs) == 0 {
		return ""
	}
	return c.errs[len(c.errs)-1]
}

// runFailingNode drives one message through a two-node workflow whose second
// node fails.
func runFailingNode(t *testing.T, dlq hermod.Sink) {
	t.Helper()

	reg := &mockRegistry{
		RunWorkflowNodeFn: func(_ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
			if node.ID == "T" {
				return nil, "", errors.New("transformation could not parse the payload")
			}
			return []hermod.Message{msg}, "", nil
		},
	}
	eng := pkgengine.NewEngine(nil, nil, nil)
	if dlq != nil {
		eng.SetDeadLetterSink(dlq)
	}

	nodeMap := map[string]*storage.WorkflowNode{
		"S": {ID: "S", Type: "source"},
		"T": {ID: "T", Type: "transformation"},
	}
	adj := map[string][]string{"S": {"T"}}
	inDegree := map[string]int{"T": 1}
	nodeIndex := map[string]int{"S": 0, "T": 1}

	srcMsg := message.AcquireMessage()
	srcMsg.SetID("m-1")

	tr := traversal.Acquire(reg, eng, "wf-dlq", nodeMap, adj, nodeIndex, nil, nil, inDegree, nil)
	srcMsg.Retain()
	tr.CurrentMessages[nodeIndex["S"]] = srcMsg
	tr.Traverse(t.Context(), "S")
}

// TestAFailedNodeDeadLettersItsMessage is the whole point.
func TestAFailedNodeDeadLettersItsMessage(t *testing.T) {
	dlq := &capturingSink{}
	runFailingNode(t, dlq)

	got := dlq.written()
	if len(got) != 1 {
		t.Fatalf("the dead-letter sink received %d message(s), want 1; a node failure "+
			"still loses the message even with a dead-letter sink configured", len(got))
	}
	if got[0] != "m-1" {
		t.Errorf("dead-lettered %q, want m-1", got[0])
	}
}

// TestTheDeadLetteredMessageCarriesTheCause. A dead-letter queue full of
// messages with no reason attached is a queue somebody has to guess about.
func TestTheDeadLetteredMessageCarriesTheCause(t *testing.T) {
	dlq := &capturingSink{}
	runFailingNode(t, dlq)

	if got := dlq.lastError(); got == "" {
		t.Error("the dead-lettered message carries no error; whoever drains the queue " +
			"has no way to know why any of it is there")
	} else if got != "transformation could not parse the payload" {
		t.Errorf("the recorded cause is %q, which is not the node's error", got)
	}
}

// TestNoDeadLetterSinkIsStillSafe. Most workflows have none; the traversal must
// carry on rather than panic on a nil sink.
func TestNoDeadLetterSinkIsStillSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a node failure panicked with no dead-letter sink configured: %v", r)
		}
	}()
	runFailingNode(t, nil)
}

// TestASucceedingNodeIsNotDeadLettered: only failures go to the queue, or the
// dead-letter sink becomes a copy of the pipeline.
func TestASucceedingNodeIsNotDeadLettered(t *testing.T) {
	dlq := &capturingSink{}

	eng := pkgengine.NewEngine(nil, nil, nil)
	eng.SetDeadLetterSink(dlq)

	nodeMap := map[string]*storage.WorkflowNode{
		"S": {ID: "S", Type: "source"},
		"T": {ID: "T", Type: "transformation"},
	}
	adj := map[string][]string{"S": {"T"}}
	inDegree := map[string]int{"T": 1}
	nodeIndex := map[string]int{"S": 0, "T": 1}

	srcMsg := message.AcquireMessage()
	srcMsg.SetID("m-ok")

	tr := traversal.Acquire(&mockRegistry{}, eng, "wf-ok", nodeMap, adj, nodeIndex, nil, nil, inDegree, nil)
	srcMsg.Retain()
	tr.CurrentMessages[nodeIndex["S"]] = srcMsg
	tr.Traverse(t.Context(), "S")

	if got := dlq.written(); len(got) != 0 {
		t.Errorf("a workflow that succeeded dead-lettered %d message(s)", len(got))
	}
}
