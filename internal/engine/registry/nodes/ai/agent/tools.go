package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/llm"
)

// toolResultField is where a read tool's transformer is told to put its
// result. It is forced, so the model never chooses where a lookup writes.
const toolResultField = "result"

// maxToolResult bounds what one tool call sends back to the model.
const maxToolResult = 16 << 10

// invoke runs an allowed tool with arguments already bound to its schema.
// Failures become error results for the model, never a failed node: the
// model can try something else.
func (r *run) invoke(ctx context.Context, t tool, c llm.ToolCall, args map[string]any) llm.ToolResult {
	var content string
	var err error
	switch t.Kind {
	case kindSink:
		err = r.write(ctx, t, c, args)
		content = `{"status":"written"}`
	case kindMCP:
		return r.callRemote(ctx, t, c, args)
	default:
		content, err = r.lookup(ctx, t, args)
	}
	if err != nil {
		return errResult(c, err.Error())
	}
	return llm.ToolResult{CallID: c.ID, Name: c.Name, Content: clipTo(content, maxToolResult)}
}

// callRemote runs an mcp tool: always the one remote tool the node names, on
// the server it names. What the server returns goes back to the model as a
// tool result, which the system prompt and the allow-list treat as data: a
// result that asks for another tool cannot get one that is not allowed, nor
// skip an approval.
func (r *run) callRemote(ctx context.Context, t tool, c llm.ToolCall, args map[string]any) llm.ToolResult {
	res, err := r.mcp.Call(ctx, t.endpoint, t.Remote, args)
	if err != nil {
		return errResult(c, err.Error())
	}
	if res.IsError {
		return errResult(c, clipTo(res.Text, maxToolResult))
	}
	return llm.ToolResult{CallID: c.ID, Name: c.Name, Content: clipTo(res.Text, maxToolResult)}
}

// toolMessage is a fresh message holding only the call's arguments. It runs
// in the vhost of the message being processed, so {{secret("X")}} in the
// tool's fixed config resolves to that vhost's secret; nothing else of the
// original message (metadata such as a reply id included) comes along.
func (r *run) toolMessage(id string, args map[string]any) *message.DefaultMessage {
	m := message.AcquireMessage()
	m.SetID(id)
	if v, ok := r.msg.(hermod.VHostScoped); ok {
		m.SetVHost(v.VHost())
	}
	for k, v := range args {
		m.SetData(k, v)
	}
	return m
}

// lookup runs a read tool's transformer with the tool's fixed config.
func (r *run) lookup(ctx context.Context, t tool, args map[string]any) (string, error) {
	m := r.toolMessage(r.msg.ID(), args)
	defer m.Release()
	cfg := maps.Clone(t.Config)
	if cfg == nil {
		cfg = map[string]any{}
	}
	cfg["targetField"] = toolResultField
	out, err := r.nctx.ApplyTransformation(ctx, m, t.Kind, cfg)
	if err != nil {
		return "", err
	}
	if out == nil {
		return "", errors.New("the lookup returned nothing")
	}
	b, err := json.Marshal(out.Data()[toolResultField])
	if err != nil {
		return "", fmt.Errorf("the lookup result cannot be encoded: %w", err)
	}
	return string(b), nil
}

// write delivers the call's arguments to the tool's sink node. The message id
// is derived from the original message and the call, so a sink that keys on
// it treats a retried call as the same record.
func (r *run) write(ctx context.Context, t tool, c llm.ToolCall, args map[string]any) error {
	sink, ok := r.nctx.GetSink(r.workflowID, t.NodeID)
	if !ok {
		return fmt.Errorf("sink node %s is not running in this workflow", t.NodeID)
	}
	m := r.toolMessage(r.msg.ID()+":"+c.ID, args)
	defer m.Release()
	return sink.Write(ctx, m)
}
