package http

import (
	"maps"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/workflow/redact"
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

func agentTools(tools ...map[string]any) []any {
	out := make([]any, len(tools))
	for i, t := range tools {
		out[i] = t
	}
	return out
}

func TestAINodeIssues_AgentNeedsProviderGoalAndTools(t *testing.T) {
	lookup := map[string]any{"name": "find", "kind": "db_lookup"}
	cases := map[string]map[string]any{
		"no provider": {"goal": "g", "tools": agentTools(lookup)},
		"no goal":     {"provider": "ollama", "tools": agentTools(lookup)},
		"no tools":    {"provider": "ollama", "goal": "g"},
		"empty tools": {"provider": "ollama", "goal": "g", "tools": []any{}},
		"empty JSON":  {"provider": "ollama", "goal": "g", "tools": "[]"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			issues := aiIssues(storage.WorkflowNode{ID: "a", Type: "ai_agent", Config: cfg})
			if len(issues) == 0 || issues[0].Severity != "error" || issues[0].NodeID != "a" {
				t.Fatalf("issues = %+v", issues)
			}
		})
	}
}

func TestAINodeIssues_AgentWithReadToolsIsFine(t *testing.T) {
	issues := aiIssues(
		storage.WorkflowNode{ID: "a", Type: "ai_agent", Config: map[string]any{
			"provider": "ollama", "goal": "g", "tools": agentTools(map[string]any{"name": "find", "kind": "db_lookup"}),
		}},
		storage.WorkflowNode{ID: "b", Type: "ai_agent", Config: map[string]any{
			"provider": "ollama", "prompt": "g", "tools": `[{"name":"t","kind":"sink","nodeId":"k"}]`,
		}},
		storage.WorkflowNode{ID: "k", Type: "sink"},
	)
	if len(issues) != 0 {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestAINodeIssues_WriteToolWithoutApprovalIsWarned(t *testing.T) {
	issues := aiIssues(
		storage.WorkflowNode{ID: "a", Type: "ai_agent", Config: map[string]any{
			"provider": "ollama", "goal": "g", "tools": agentTools(
				map[string]any{"name": "send", "kind": "sink", "nodeId": "k", "requireApproval": false},
				map[string]any{"name": "post", "kind": "api_lookup", "write": true, "requireApproval": false},
				map[string]any{"name": "gated", "kind": "sink", "nodeId": "k"},
				map[string]any{"name": "read", "kind": "db_lookup", "requireApproval": false},
			),
		}},
		storage.WorkflowNode{ID: "k", Type: "sink"},
	)
	if len(issues) != 2 {
		t.Fatalf("want one warning per ungated write tool, got %+v", issues)
	}
	for i, tool := range []string{"send", "post"} {
		if issues[i].Severity != "warning" || !strings.Contains(issues[i].Message, tool) || issues[i].NodeID != "a" {
			t.Errorf("issue %d = %+v", i, issues[i])
		}
	}
}

func TestAINodeIssues_SinkToolMustNameASinkNode(t *testing.T) {
	issues := aiIssues(
		storage.WorkflowNode{ID: "a", Type: "ai_agent", Config: map[string]any{
			"provider": "ollama", "goal": "g", "tools": agentTools(map[string]any{"name": "send", "kind": "sink", "nodeId": "nope"}),
		}},
	)
	if len(issues) != 1 || issues[0].Severity != "error" || !strings.Contains(issues[0].Message, "nope") {
		t.Fatalf("issues = %+v", issues)
	}
}

func agentNodeWith(tools ...map[string]any) storage.WorkflowNode {
	return storage.WorkflowNode{ID: "a", Type: "ai_agent", Config: map[string]any{
		"provider": "ollama", "goal": "g", "tools": agentTools(tools...),
	}}
}

func TestAINodeIssues_MCPToolIsFine(t *testing.T) {
	issues := aiIssues(agentNodeWith(map[string]any{
		"name": "read_x", "kind": "mcp", "tool": "read_x",
		"server": map[string]any{
			"url":     `{{secret("MCP_URL")}}`,
			"headers": map[string]any{"Authorization": `Bearer {{secret("MCP_TOKEN")}}`, "X-Tenant": "acme"},
		},
	}))
	if len(issues) != 0 {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestAINodeIssues_MCPToolNeedsServerURLAndTool(t *testing.T) {
	cases := map[string]map[string]any{
		"no server":    {"name": "t", "kind": "mcp", "tool": "read_x"},
		"no url":       {"name": "t", "kind": "mcp", "tool": "read_x", "server": map[string]any{"headers": map[string]any{}}},
		"no tool":      {"name": "t", "kind": "mcp", "server": map[string]any{"url": "https://mcp.example/mcp"}},
		"bad scheme":   {"name": "t", "kind": "mcp", "tool": "x", "server": map[string]any{"url": "file:///etc/passwd"}},
		"blank tool":   {"name": "t", "kind": "mcp", "tool": "  ", "server": map[string]any{"url": "https://mcp.example/mcp"}},
		"bad url type": {"name": "t", "kind": "mcp", "tool": "x", "server": map[string]any{"url": 3}},
	}
	for name, tool := range cases {
		t.Run(name, func(t *testing.T) {
			issues := aiIssues(agentNodeWith(tool))
			if len(issues) != 1 || issues[0].Severity != "error" || issues[0].NodeID != "a" || !strings.Contains(issues[0].Message, "'t'") {
				t.Fatalf("issues = %+v", issues)
			}
		})
	}
}

func TestAINodeIssues_MCPPlaintextCredentialHeadersAreWarned(t *testing.T) {
	issues := aiIssues(agentNodeWith(map[string]any{
		"name": "t", "kind": "mcp", "tool": "x",
		"server": map[string]any{"url": "https://mcp.example/mcp", "headers": map[string]any{
			"Authorization":  "Bearer abc123secret",
			"X-Api-Key":      "k-998877",
			"X-Access-Token": "tok-445566",
			"X-Tenant":       "acme",
			"X-Other-Key":    `{{secret("OTHER")}}`,
		}},
	}))
	if len(issues) != 3 {
		t.Fatalf("want one warning per plaintext credential header, got %+v", issues)
	}
	for _, is := range issues {
		if is.Severity != "warning" || is.NodeID != "a" || !strings.Contains(is.Recommendation, `{{secret(`) {
			t.Errorf("issue = %+v", is)
		}
		for _, leak := range []string{"abc123secret", "k-998877", "tok-445566"} {
			if strings.Contains(is.Message+is.Recommendation, leak) {
				t.Errorf("the warning repeats the credential: %+v", is)
			}
		}
	}
}

func TestAINodeIssues_MCPToolWithoutApprovalIsWarned(t *testing.T) {
	issues := aiIssues(agentNodeWith(map[string]any{
		"name": "t", "kind": "mcp", "tool": "x", "requireApproval": false,
		"server": map[string]any{"url": "https://mcp.example/mcp"},
	}))
	if len(issues) != 1 || issues[0].Severity != "warning" || !strings.Contains(issues[0].Message, "'t'") {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestAINodeIssues_RetrieveNeedsItsStore(t *testing.T) {
	base := func(extra map[string]any) storage.WorkflowNode {
		cfg := map[string]any{"transType": "ai_retrieve", "provider": "ollama", "query": "{{.q}}"}
		maps.Copy(cfg, extra)
		return storage.WorkflowNode{ID: "r", Type: "transformation", Config: cfg}
	}
	bad := map[string]map[string]any{
		"no store":          {},
		"unknown store":     {"store": "qdrant"},
		"pgvector no conn":  {"store": "pgvector", "table": "docs"},
		"pgvector no table": {"store": "pgvector", "connectionString": `{{secret("PG")}}`},
		"pinecone no host":  {"store": "pinecone"},
		"no query or field": {"store": "pinecone", "indexHost": "https://x", "query": ""},
	}
	for name, extra := range bad {
		t.Run(name, func(t *testing.T) {
			issues := aiIssues(base(extra))
			if len(issues) == 0 || issues[0].Severity != "error" {
				t.Fatalf("issues = %+v", issues)
			}
		})
	}
	good := []map[string]any{
		{"store": "pgvector", "connectionString": `{{secret("PG")}}`, "table": "docs"},
		{"store": "pinecone", "indexHost": "https://idx", "query": "", "queryField": "q"},
	}
	for _, extra := range good {
		if issues := aiIssues(base(extra)); len(issues) != 0 {
			t.Errorf("%v: issues = %+v", extra, issues)
		}
	}
}

// An imported export has its keys replaced. Validation says what happened and
// what to do, rather than calling the placeholder a stored key.
func TestAINodeIssues_RedactedKeyFromAnExportAsksForTheKey(t *testing.T) {
	issues := aiIssues(storage.WorkflowNode{ID: "n1", Type: "ai_classify", Config: map[string]any{
		"provider": "openai", "apiKey": redact.Placeholder, "labels": "a,b",
	}})
	if len(issues) != 1 || issues[0].Severity != "warning" ||
		!strings.Contains(issues[0].Message, "removed when the workflow was exported") {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestAINodeIssues_MemoryConfig(t *testing.T) {
	ok := aiIssues(storage.WorkflowNode{ID: "m", Type: "transformation", Config: map[string]any{
		"transType": "ai_prompt", "provider": "ollama", "prompt": "x", "memory": map[string]any{"maxTurns": 5.0, "ttl": "1h"},
	}})
	if len(ok) != 0 {
		t.Fatalf("valid memory: %+v", ok)
	}
	bad := aiIssues(storage.WorkflowNode{ID: "m", Type: "transformation", Config: map[string]any{
		"transType": "ai_prompt", "provider": "ollama", "prompt": "x", "memory": map[string]any{"maxTurns": 500.0},
	}})
	if len(bad) != 1 || bad[0].Severity != "error" || !strings.Contains(bad[0].Message, "maxTurns") {
		t.Fatalf("invalid memory: %+v", bad)
	}
}
