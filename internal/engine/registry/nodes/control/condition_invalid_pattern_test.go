package control

// A condition node whose regex does not compile took the false branch for
// every message, silently. Nothing was logged, no error was returned, and the
// workflow stayed green while it dropped all of its traffic — a typo in a
// filter was indistinguishable from "nothing matched".
//
// It has to fail the way every other node failure does, so the engine's
// existing machinery handles it: dead-letter the message if there is a DLQ,
// and say so loudly if there is not.

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// recordingCtx is a NodeContext that captures broadcast logs and otherwise
// does nothing. Only the two methods a condition node reaches are meaningful.
type recordingCtx struct {
	interfaces.NodeContext // nil: any other call faults loudly rather than passing silently
	mu                     sync.Mutex
	logs                   []string
}

func (c *recordingCtx) BroadcastLog(workflowID, level, msg, msgID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logs = append(c.logs, level+": "+msg)
}

func (c *recordingCtx) EvaluateConditions(msg hermod.Message, conditions []map[string]any) bool {
	// The real registry delegates to the evaluator; a condition that compiles
	// is not what this test is about.
	return true
}

func (c *recordingCtx) logged() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.logs...)
}

func conditionNode(field, op, value string) *storage.WorkflowNode {
	return &storage.WorkflowNode{
		ID:   "cond",
		Type: "condition",
		Config: map[string]any{
			"field":    field,
			"operator": op,
			"value":    value,
		},
	}
}

func TestConditionNodeFailsLoudlyOnAnUncompilablePattern(t *testing.T) {
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetData("status", "active")

	nctx := &recordingCtx{}
	node := conditionNode("status", "regex", `[unclosed`)

	_, branch, err := (&ConditionNode{}).Execute(context.Background(), nctx, "wf", node, msg)

	if err == nil {
		t.Fatalf("an uncompilable pattern returned branch %q and no error: the message is dropped and nothing says why", branch)
	}
	if !strings.Contains(err.Error(), "[unclosed") {
		t.Errorf("error %q does not name the pattern, so an operator cannot find it", err)
	}

	var sawError bool
	for _, l := range nctx.logged() {
		if strings.HasPrefix(l, "ERROR") && strings.Contains(l, "[unclosed") {
			sawError = true
		}
	}
	if !sawError {
		t.Errorf("no ERROR was broadcast for an invalid pattern; logs were %v", nctx.logged())
	}
}

// A pattern that compiles must keep taking its branch, error-free.
func TestConditionNodeStillBranchesOnAValidPattern(t *testing.T) {
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetData("status", "active")

	nctx := &recordingCtx{}
	node := conditionNode("status", "regex", `^active$`)

	msgs, branch, err := (&ConditionNode{}).Execute(context.Background(), nctx, "wf", node, msg)
	if err != nil {
		t.Fatalf("a valid pattern returned an error: %v", err)
	}
	if branch != "true" {
		t.Errorf("branch = %q, want %q", branch, "true")
	}
	if len(msgs) != 1 {
		t.Errorf("returned %d messages, want 1", len(msgs))
	}
}

// A templated pattern is resolved per message, so it cannot be judged up
// front and must not be treated as broken.
func TestConditionNodeAcceptsATemplatedPattern(t *testing.T) {
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetData("status", "active")
	msg.SetData("pattern", "^active$")

	nctx := &recordingCtx{}
	node := conditionNode("status", "regex", `{{.pattern}}`)

	if _, _, err := (&ConditionNode{}).Execute(context.Background(), nctx, "wf", node, msg); err != nil {
		t.Fatalf("a templated pattern was rejected up front: %v", err)
	}
}
