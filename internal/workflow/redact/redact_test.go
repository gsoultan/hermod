package redact_test

import (
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/workflow/redact"
)

func TestAIKeysReplacesPlaintextKeysOnAINodesOnly(t *testing.T) {
	nodes := []storage.WorkflowNode{
		{ID: "prompt", Type: "transformation", Config: map[string]any{
			"transType": "ai_prompt", "provider": "openai", "apiKey": "sk-live-123", "fallbackApiKey": "sk-fb-456",
		}},
		{ID: "classify", Type: "ai_classify", Config: map[string]any{"provider": "anthropic", "apiKey": "  sk-ant-789 "}},
		{ID: "templated", Type: "transformation", Config: map[string]any{
			"transType": "ai_extract", "apiKey": `{{secret("OPENAI_KEY")}}`,
		}},
		{ID: "legacy", Type: "transformation", Config: map[string]any{"transType": "ai_enrichment", "apiKey": "sk-old"}},
		{ID: "blank", Type: "ai_classify", Config: map[string]any{"apiKey": ""}},
		{ID: "not-ai", Type: "transformation", Config: map[string]any{"transType": "api_lookup", "apiKey": "kept"}},
	}

	out := redact.AIKeys(nodes)

	want := map[string]map[string]any{
		"prompt":    {"apiKey": redact.Placeholder, "fallbackApiKey": redact.Placeholder},
		"classify":  {"apiKey": redact.Placeholder},
		"templated": {"apiKey": `{{secret("OPENAI_KEY")}}`},
		"legacy":    {"apiKey": redact.Placeholder},
		"blank":     {"apiKey": ""},
		"not-ai":    {"apiKey": "kept"},
	}
	for _, n := range out {
		for k, v := range want[n.ID] {
			if n.Config[k] != v {
				t.Errorf("node %s: %s = %q, want %q", n.ID, k, n.Config[k], v)
			}
		}
	}
	if out[0].Config["provider"] != "openai" || out[0].Config["transType"] != "ai_prompt" {
		t.Errorf("redaction touched other settings: %v", out[0].Config)
	}

	// The caller's workflow is not edited: the export is a copy.
	if nodes[0].Config["apiKey"] != "sk-live-123" || nodes[1].Config["apiKey"] != "  sk-ant-789 " {
		t.Errorf("the stored nodes were modified: %v / %v", nodes[0].Config, nodes[1].Config)
	}
}
