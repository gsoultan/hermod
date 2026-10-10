package agent

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/llm"
)

// scriptedProvider answers each Chat with the next scripted turn and keeps
// every request it was sent, so a test can read what the model was shown.
type scriptedProvider struct {
	mu    sync.Mutex
	turns []func(llm.ChatRequest) llm.ChatResponse
	reqs  []llm.ChatRequest
}

func (p *scriptedProvider) Name() string { return "fake" }

func (p *scriptedProvider) Chat(_ context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reqs = append(p.reqs, req)
	if len(p.turns) == 0 {
		return llm.ChatResponse{}, errors.New("scriptedProvider: no more turns")
	}
	turn := p.turns[0]
	p.turns = p.turns[1:]
	resp := turn(req)
	resp.Provider = "fake"
	return resp, nil
}

func (p *scriptedProvider) requests() []llm.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]llm.ChatRequest(nil), p.reqs...)
}

// say is a turn that ends with a final answer.
func say(text string) func(llm.ChatRequest) llm.ChatResponse {
	return func(llm.ChatRequest) llm.ChatResponse {
		return llm.ChatResponse{Text: text, StopReason: llm.StopEnd, Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}}
	}
}

// call is a turn that asks for one tool.
func call(id, name, input string) func(llm.ChatRequest) llm.ChatResponse {
	return func(llm.ChatRequest) llm.ChatResponse {
		return llm.ChatResponse{
			ToolCalls:  []llm.ToolCall{{ID: id, Name: name, Input: json.RawMessage(input)}},
			StopReason: llm.StopToolUse,
			Usage:      llm.Usage{InputTokens: 10, OutputTokens: 5},
		}
	}
}

func newNode(p llm.Provider) *Node {
	return &Node{providerFor: func(map[string]any, hermod.Message) (llm.Provider, string, error) {
		return p, "fake-model", nil
	}}
}

// recordingSink keeps what was written to it.
type recordingSink struct {
	mu   sync.Mutex
	msgs []map[string]any
	ids  []string
}

func (s *recordingSink) Write(_ context.Context, m hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, maps.Clone(m.Data()))
	s.ids = append(s.ids, m.ID())
	return nil
}
func (s *recordingSink) Ping(context.Context) error { return nil }
func (s *recordingSink) Close() error               { return nil }

func (s *recordingSink) writes() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.msgs...)
}

// lookupCall is one ApplyTransformation the agent made.
type lookupCall struct {
	transType string
	config    map[string]any
	data      map[string]any
}

// approvalStore keeps the approvals the agent records.
type approvalStore struct {
	interfaces.RegistryStorage
	mu        sync.Mutex
	approvals []storage.Approval
}

func (s *approvalStore) CreateApproval(_ context.Context, app storage.Approval) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approvals = append(s.approvals, app)
	return nil
}

func (s *approvalStore) list() []storage.Approval {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storage.Approval(nil), s.approvals...)
}

// fakeNodeContext is the registry as the agent sees it: lookups answer from
// lookupResult, sinks are recordingSinks by node id, approvals are kept.
type fakeNodeContext struct {
	interfaces.NodeContext
	mu           sync.Mutex
	lookups      []lookupCall
	lookupResult any
	sinks        map[string]*recordingSink
	store        *approvalStore
}

func newFakeNodeContext() *fakeNodeContext {
	return &fakeNodeContext{
		lookupResult: map[string]any{"name": "Ada"},
		sinks:        map[string]*recordingSink{"ticket-sink": {}, "delete-sink": {}},
		store:        &approvalStore{},
	}
}

func (f *fakeNodeContext) ApplyTransformation(_ context.Context, msg hermod.Message, transType string, config map[string]any) (hermod.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups = append(f.lookups, lookupCall{transType: transType, config: maps.Clone(config), data: maps.Clone(msg.Data())})
	target, _ := config["targetField"].(string)
	msg.SetData(target, f.lookupResult)
	return msg, nil
}

func (f *fakeNodeContext) lookupCalls() []lookupCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lookupCall(nil), f.lookups...)
}

func (f *fakeNodeContext) GetSink(_, nodeID string) (hermod.Sink, bool) {
	s, ok := f.sinks[nodeID]
	return s, ok
}

func (f *fakeNodeContext) Storage() interfaces.RegistryStorage { return f.store }

func (f *fakeNodeContext) BroadcastLog(string, string, string, string) {}

// agentNode is an ai_agent node with one read tool (find_customer, a
// db_lookup) and two write tools (create_ticket, delete_all) that default to
// approval.
func agentNode(extra map[string]any) *storage.WorkflowNode {
	cfg := map[string]any{
		"provider": "fake",
		"goal":     "Help the customer.",
		"tools": []any{
			map[string]any{
				"name": "find_customer", "kind": "db_lookup", "description": "Find a customer by email.",
				"parameters": []any{map[string]any{"name": "email", "type": "string", "required": true}},
				"config":     map[string]any{"sourceId": "crm", "table": "customers", "keyColumn": "email", "keyField": "email"},
			},
			map[string]any{
				"name": "create_ticket", "kind": "sink", "nodeId": "ticket-sink", "description": "Open a ticket.",
				"parameters": []any{map[string]any{"name": "subject", "type": "string", "required": true}},
			},
		},
	}
	maps.Copy(cfg, extra)
	return &storage.WorkflowNode{ID: "agent-1", Type: "ai_agent", Config: cfg}
}

func inputMessage(t *testing.T, data map[string]any) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	m.SetID("msg-1")
	for k, v := range data {
		m.SetData(k, v)
	}
	t.Cleanup(m.Release)
	return m
}

// lastToolResults are the tool results in the newest request the model saw.
func lastToolResults(p *scriptedProvider) []llm.ToolResult {
	reqs := p.requests()
	if len(reqs) == 0 {
		return nil
	}
	msgs := reqs[len(reqs)-1].Messages
	for i := len(msgs) - 1; i >= 0; i-- {
		if len(msgs[i].ToolResults) > 0 {
			return msgs[i].ToolResults
		}
	}
	return nil
}
