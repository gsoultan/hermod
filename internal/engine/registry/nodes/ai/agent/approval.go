package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/transformer/genai"
	"github.com/gsoultan/hermod/pkg/llm"
)

// decisionApproved is the only verdict that runs a held call.
const decisionApproved = "approved"

// suspend records an approval for the pending call and takes the message out
// of the pipeline, as the approval node does. The loop's state travels inside
// the approval record, and the registry hands that record back to
// ResumeApproval when a person decides.
func (r *run) suspend(ctx context.Context) ([]hermod.Message, string, error) {
	pt := r.st.Pending
	c := pt.Calls[pt.Next]
	t := r.tools[c.Name]
	args, _ := t.bindArgs(c.Input)

	raw, err := json.Marshal(r.st)
	if err != nil {
		return r.fail(fmt.Errorf("could not save the agent's state for approval: %w", err))
	}
	store := r.nctx.Storage()
	if store == nil {
		return r.fail(errors.New("no storage, so the approval a write tool needs could never be recorded"))
	}
	r.st.Transcript.write(r.msg, r.cfg.transcript, "awaiting_approval", r.st.Steps, r.st.Usage, nil)

	data := maps.Clone(r.msg.Data())
	data[stateField] = string(raw)
	pending := map[string]any{
		"tool":        c.Name,
		"description": t.Description,
		"call_id":     c.ID,
		"arguments":   args,
	}
	if t.Kind == kindMCP {
		// The reviewer sees which remote tool would run, not just the
		// node's name for it.
		pending["remote_tool"] = t.Remote
	}
	data[PendingCallField] = pending
	app := storage.Approval{
		ID:         uuid.New().String(),
		WorkflowID: r.workflowID,
		NodeID:     r.node.ID,
		MessageID:  r.msg.ID(),
		Payload:    r.msg.Payload(),
		Metadata:   r.msg.Metadata(),
		Data:       data,
		Status:     "pending",
		CreatedAt:  time.Now(),
	}
	if err := store.CreateApproval(ctx, app); err != nil {
		return r.fail(fmt.Errorf("could not record the approval for tool %q, so nobody could approve it: %w", c.Name, err))
	}
	r.nctx.BroadcastLog(r.workflowID, "INFO",
		fmt.Sprintf("AI agent %s is waiting for approval to call %s", r.node.ID, c.Name), r.msg.ID())
	return nil, "pending", nil
}

// ResumeApproval implements interfaces.ApprovalResumer. The decision comes
// from the approval endpoint and the state from the approval record this node
// wrote; the call is checked against the node's current allow-list, so a tool
// removed while the approval waited does not run.
func (n *Node) ResumeApproval(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message, app storage.Approval, decision string) ([]hermod.Message, string, error) {
	data := msg.DataRef()
	delete(data, stateField)
	delete(data, PendingCallField)

	raw, _ := app.Data[stateField].(string)
	if raw == "" {
		return []hermod.Message{msg}, "", fmt.Errorf("ai_agent %s: approval %s carries no agent state", node.ID, app.ID)
	}
	var st state
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return []hermod.Message{msg}, "", fmt.Errorf("ai_agent %s: approval %s has unreadable agent state: %w", node.ID, app.ID, err)
	}
	if st.Pending == nil || st.Pending.Next < 0 || st.Pending.Next >= len(st.Pending.Calls) {
		return []hermod.Message{msg}, "", fmt.Errorf("ai_agent %s: approval %s holds no tool call", node.ID, app.ID)
	}
	r, err := n.newRun(nctx, workflowID, node, msg, &st)
	if err != nil {
		return []hermod.Message{msg}, "", fmt.Errorf("ai_agent %s: %w", node.ID, err)
	}
	ctx, cancel := context.WithTimeout(genai.WithWorkflow(ctx, workflowID), r.cfg.timeout)
	defer cancel()
	if err := r.prepare(ctx); err != nil {
		return r.fail(err)
	}

	c := st.Pending.Calls[st.Pending.Next]
	st.Transcript.add(entry{Step: st.Steps, Kind: "approval_decision", Tool: c.Name, CallID: c.ID, Text: decision})
	r.record(r.decide(ctx, c, decision, app.Notes))
	return r.loop(ctx)
}

// decide runs the held call if it was approved and is still allowed.
func (r *run) decide(ctx context.Context, c llm.ToolCall, decision, notes string) llm.ToolResult {
	if decision != decisionApproved {
		text := "a reviewer rejected this call"
		if notes = strings.TrimSpace(notes); notes != "" {
			text += ": " + notes
		}
		return errResult(c, text)
	}
	t, ok := r.tools[c.Name]
	if !ok {
		return errResult(c, fmt.Sprintf("tool %q is no longer available to this agent", c.Name))
	}
	args, err := t.bindArgs(c.Input)
	if err != nil {
		return errResult(c, err.Error())
	}
	return r.invoke(ctx, t, c, args)
}
