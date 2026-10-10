package builder

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/llm"
)

// fakeProvider answers every chat with a canned text and records the request.
type fakeProvider struct {
	answer string
	got    llm.ChatRequest
}

func (*fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Chat(_ context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	f.got = req
	return llm.ChatResponse{Text: f.answer, Provider: "fake", Model: "m1", StopReason: llm.StopEnd,
		Usage: llm.Usage{InputTokens: 10, OutputTokens: 20}}, nil
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBuildAsksForStructuredOutputListingOnlyRegisteredNodes(t *testing.T) {
	p := &fakeProvider{answer: `{"name":"x","nodes":[],"edges":[]}`}
	_, err := Build(t.Context(), p, Request{
		Description: "classify support emails",
		Sources:     []Ref{{ID: "src-1", Name: "Support inbox", Type: "webhook"}},
		Sinks:       []Ref{{ID: "snk-1", Name: "Slack #support", Type: "slack"}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if p.got.ResponseSchema == nil {
		t.Fatal("the request did not ask for structured output")
	}
	if !strings.Contains(p.got.Messages[0].Text, "classify support emails") {
		t.Errorf("the description is not the user turn: %+v", p.got.Messages)
	}
	for _, want := range []string{"transformation:ai_prompt", "ai_classify", "src-1", "Support inbox", "snk-1"} {
		if !strings.Contains(p.got.System, want) {
			t.Errorf("the system prompt does not mention %q", want)
		}
	}
	if strings.Contains(p.got.System, "transformation:pipeline") {
		t.Error("the prompt offers a node kind that is not registered")
	}

	// The schema constrains node types to the registered ones.
	schema, _ := json.Marshal(p.got.ResponseSchema)
	var s struct {
		Properties struct {
			Nodes struct {
				Items struct {
					Properties struct {
						Type struct {
							Enum []string `json:"enum"`
						} `json:"type"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"nodes"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		t.Fatal(err)
	}
	enum := s.Properties.Nodes.Items.Properties.Type.Enum
	for _, want := range []string{"source", "sink", "transformation", "condition", "ai_classify"} {
		if !slices.Contains(enum, want) {
			t.Errorf("node type enum %v lacks %q", enum, want)
		}
	}
}

func TestBuildTurnsTheAnswerIntoAnInactiveWorkflow(t *testing.T) {
	p := &fakeProvider{answer: jsonOf(t, map[string]any{
		"name": "Support triage",
		"nodes": []map[string]any{
			{"id": "in", "type": "source", "ref_id": "src-1", "config_json": ""},
			{"id": "cls", "type": "ai_classify", "ref_id": "", "config_json": `{"provider":"anthropic","labels":"billing,bug","apiKey":"{{secret(\"CLAUDE\")}}"}`},
			{"id": "out", "type": "sink", "ref_id": "snk-1", "config_json": "{}"},
		},
		"edges": []map[string]any{
			{"source_id": "in", "target_id": "cls", "source_handle": ""},
			{"source_id": "cls", "target_id": "out", "source_handle": "billing"},
		},
	})}
	d, err := Build(t.Context(), p, Request{
		Description: "triage", VHost: "tenant-a",
		Sources: []Ref{{ID: "src-1"}}, Sinks: []Ref{{ID: "snk-1"}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	wf := d.Workflow
	if wf.Name != "Support triage" || wf.VHost != "tenant-a" || wf.Active {
		t.Errorf("workflow = %+v", wf)
	}
	if len(wf.Nodes) != 3 || len(wf.Edges) != 2 {
		t.Fatalf("nodes %d edges %d", len(wf.Nodes), len(wf.Edges))
	}
	if wf.Nodes[0].RefID != "src-1" || wf.Nodes[1].Config["labels"] != "billing,bug" {
		t.Errorf("nodes = %+v", wf.Nodes)
	}
	if wf.Edges[1].SourceHandle != "billing" || wf.Edges[0].ID == "" || wf.Edges[0].ID == wf.Edges[1].ID {
		t.Errorf("edges = %+v", wf.Edges)
	}
	if wf.Nodes[0].X == wf.Nodes[1].X {
		t.Error("the nodes were not laid out left to right")
	}
	if len(d.Issues) != 0 {
		t.Errorf("issues = %+v", d.Issues)
	}
	if d.Usage.OutputTokens != 20 || d.Provider != "fake" {
		t.Errorf("usage %+v provider %q", d.Usage, d.Provider)
	}
}

func TestBuildReportsWhatItCannotUse(t *testing.T) {
	p := &fakeProvider{answer: "```json\n" + jsonOf(t, map[string]any{
		"name": "bad",
		"nodes": []map[string]any{
			{"id": "in", "type": "source", "ref_id": "someone-elses-source", "config_json": ""},
			{"id": "x", "type": "transformation", "ref_id": "", "config_json": `{"transType":"teleport"}`},
			{"id": "y", "type": "made_up", "ref_id": "", "config_json": "{}"},
			{"id": "z", "type": "condition", "ref_id": "", "config_json": "not json"},
		},
		"edges": []map[string]any{{"source_id": "in", "target_id": "ghost", "source_handle": ""}},
	}) + "\n```"}
	d, err := Build(t.Context(), p, Request{Description: "x", Sources: []Ref{{ID: "src-1"}}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if d.Workflow.Nodes[0].RefID != "" {
		t.Error("a source the caller was not offered was kept in the draft")
	}
	if len(d.Workflow.Edges) != 0 {
		t.Errorf("an edge to a missing node was kept: %+v", d.Workflow.Edges)
	}
	byNode := map[string]bool{}
	for _, is := range d.Issues {
		if is.Severity == "error" {
			byNode[is.NodeID] = true
		}
	}
	for _, id := range []string{"in", "x", "y", "z"} {
		if !byNode[id] {
			t.Errorf("no error reported for node %s; issues: %+v", id, d.Issues)
		}
	}
}

func TestBuildRefusesAnEmptyDescriptionAndAnUnreadableAnswer(t *testing.T) {
	if _, err := Build(t.Context(), &fakeProvider{}, Request{Description: "  "}); !errors.Is(err, ErrEmptyDescription) {
		t.Errorf("empty description: %v", err)
	}
	if _, err := Build(t.Context(), &fakeProvider{answer: "I cannot help"}, Request{Description: "x"}); err == nil {
		t.Error("an answer that is not a workflow was accepted")
	}
}
