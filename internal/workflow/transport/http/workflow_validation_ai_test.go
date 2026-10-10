package http

import (
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

func aiIssues(nodes ...storage.WorkflowNode) []ValidationIssue {
	return aiNodeIssues(storage.Workflow{Name: "w", Nodes: nodes})
}

func TestAINodeIssues_PlaintextKeyIsWarned(t *testing.T) {
	issues := aiIssues(storage.WorkflowNode{ID: "n1", Type: "transformation", Config: map[string]any{
		"transType": "ai_prompt", "provider": "openai", "apiKey": "sk-live-123", "prompt": "x",
	}})
	if len(issues) != 1 || issues[0].Severity != "warning" || !strings.Contains(issues[0].Recommendation, `{{secret(`) {
		t.Fatalf("issues = %+v", issues)
	}
	if strings.Contains(issues[0].Message+issues[0].Recommendation, "sk-live") {
		t.Fatal("the warning repeats the key")
	}
}

func TestAINodeIssues_SecretReferenceAndKeylessAreFine(t *testing.T) {
	issues := aiIssues(
		storage.WorkflowNode{ID: "n1", Type: "transformation", Config: map[string]any{"transType": "ai_extract", "provider": "anthropic", "apiKey": `{{secret("CLAUDE_KEY")}}`, "schema": "{}"}},
		storage.WorkflowNode{ID: "n2", Type: "ai_classify", Config: map[string]any{"provider": "ollama", "labels": "a,b"}},
	)
	if len(issues) != 0 {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestAINodeIssues_MissingSettingsAreErrors(t *testing.T) {
	cases := []storage.WorkflowNode{
		{ID: "p", Type: "transformation", Config: map[string]any{"transType": "ai_prompt", "provider": "ollama"}},
		{ID: "e", Type: "transformation", Config: map[string]any{"transType": "ai_extract", "provider": "ollama"}},
		{ID: "c", Type: "ai_classify", Config: map[string]any{"provider": "ollama"}},
		{ID: "x", Type: "transformation", Config: map[string]any{"transType": "ai_prompt", "prompt": "x"}},
	}
	for _, n := range cases {
		issues := aiIssues(n)
		if len(issues) == 0 || issues[0].Severity != "error" || issues[0].NodeID != n.ID {
			t.Errorf("%s: issues = %+v", n.ID, issues)
		}
	}
}
