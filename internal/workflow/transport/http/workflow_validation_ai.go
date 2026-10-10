package http

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/workflow/redact"
	"github.com/gsoultan/hermod/pkg/comm/transformer/genai/memory"
)

// aiNodeTypes are the nodes that call a language model, with the setting
// each cannot run without.
var aiNodeTypes = map[string]struct{ key, what string }{
	"ai_prompt":   {"prompt", "a prompt"},
	"ai_extract":  {"schema", "a JSON Schema"},
	"ai_classify": {"labels", "at least one label"},
	"ai_embed":    {"", ""},
	"ai_agent":    {"", ""},
	"ai_retrieve": {"", ""},
}

// keylessAIProviders run without an API key.
var keylessAIProviders = map[string]bool{"ollama": true, "openai_compatible": true}

// aiNodeIssues checks the AI nodes: a missing provider or required setting is
// an error (every message would fail), and an API key typed into the node is a
// warning, because it is stored in the workflow and travels with every export.
func aiNodeIssues(wf storage.Workflow) (issues []ValidationIssue) {
	for _, n := range wf.Nodes {
		nodeType := nodeTransformationType(n)
		req, isAI := aiNodeTypes[nodeType]
		if !isAI {
			continue
		}
		str := func(k string) string { s, _ := n.Config[k].(string); return strings.TrimSpace(s) }
		provider := strings.ToLower(str("provider"))
		if provider == "" {
			issues = append(issues, ValidationIssue{
				Severity:       "error",
				Message:        fmt.Sprintf("AI node '%s' has no provider.", n.ID),
				Recommendation: "Choose the AI provider (Claude, OpenAI, Gemini, DeepSeek, Ollama, ...) and model in the node's settings.",
				NodeID:         n.ID,
			})
			continue
		}
		if req.key != "" && isMissing(n.Config[req.key]) {
			issues = append(issues, ValidationIssue{
				Severity:       "error",
				Message:        fmt.Sprintf("AI node '%s' (%s) needs %s.", n.ID, nodeType, req.what),
				Recommendation: "Open the node and fill in its required settings; until then every message fails at this node.",
				NodeID:         n.ID,
			})
		}
		switch nodeType {
		case "ai_agent":
			issues = append(issues, agentNodeIssues(wf, n)...)
		case "ai_retrieve":
			issues = append(issues, retrieveNodeIssues(n)...)
		}
		if issue, bad := memoryIssue(n, nodeType); bad {
			issues = append(issues, issue)
		}
		key := str("apiKey")
		switch {
		case key == redact.Placeholder:
			issues = append(issues, ValidationIssue{
				Severity:       "warning",
				Message:        fmt.Sprintf("AI node '%s' has no API key: it was removed when the workflow was exported.", n.ID),
				Recommendation: `Save the key as a vhost secret and set the node's API key to {{secret("NAME")}}; until then every call to the provider is refused.`,
				NodeID:         n.ID,
			})
		case key != "" && !strings.Contains(key, "{{") && !keylessAIProviders[provider]:
			issues = append(issues, ValidationIssue{
				Severity: "warning",
				Message:  fmt.Sprintf("AI node '%s' stores its API key in the workflow.", n.ID),
				Recommendation: `Save the key as a vhost secret and set the node's API key to {{secret("NAME")}}. ` +
					"A key typed into the node is saved with the workflow and its version history (exports replace it with a placeholder).",
				NodeID: n.ID,
			})
		}
	}
	return issues
}

// agentNodeIssues checks an ai_agent node: it needs a goal and at least one
// tool, a sink tool must name a sink node of this workflow, and a write tool
// that skips approval is called out, because a model persuaded by message
// data can then change things with nobody looking.
func agentNodeIssues(wf storage.Workflow, n storage.WorkflowNode) (issues []ValidationIssue) {
	needs := func(what string) ValidationIssue {
		return ValidationIssue{
			Severity:       "error",
			Message:        fmt.Sprintf("AI agent '%s' needs %s.", n.ID, what),
			Recommendation: "Open the node and fill in its required settings; until then every message fails at this node.",
			NodeID:         n.ID,
		}
	}
	if isMissing(n.Config["goal"]) && isMissing(n.Config["prompt"]) {
		issues = append(issues, needs("a goal"))
	}
	tools := agentToolList(n.Config["tools"])
	if len(tools) == 0 {
		return append(issues, needs("at least one tool"))
	}
	sinks := map[string]bool{}
	for _, wn := range wf.Nodes {
		if wn.Type == "sink" {
			sinks[wn.ID] = true
		}
	}
	for _, t := range tools {
		name, _ := t["name"].(string)
		kind, _ := t["kind"].(string)
		if nodeID, _ := t["nodeId"].(string); kind == "sink" && !sinks[nodeID] {
			issues = append(issues, ValidationIssue{
				Severity:       "error",
				Message:        fmt.Sprintf("AI agent '%s' tool '%s' writes to node '%s', which is not a sink node of this workflow.", n.ID, name, nodeID),
				Recommendation: "Add the sink node the tool should write to and pick it in the tool's settings.",
				NodeID:         n.ID,
			})
		}
		write := kind == "sink" || t["write"] == true
		if write && t["requireApproval"] == false {
			issues = append(issues, ValidationIssue{
				Severity: "warning",
				Message:  fmt.Sprintf("AI agent '%s' can call write tool '%s' without anyone approving it.", n.ID, name),
				Recommendation: "Message data can steer a model. Leave approval on for tools that change other systems " +
					"unless every call is safe to make unreviewed.",
				NodeID: n.ID,
			})
		}
	}
	return issues
}

// agentToolList reads an agent's tools, given as a list or as its JSON text.
func agentToolList(raw any) []map[string]any {
	if s, ok := raw.(string); ok {
		raw = nil
		_ = json.Unmarshal([]byte(s), &raw)
	}
	items, _ := raw.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// retrieveStoreKeys are the settings each vector store cannot run without.
var retrieveStoreKeys = map[string][]struct{ key, what string }{
	"pgvector": {{"connectionString", "a connection string"}, {"table", "a table"}},
	"pinecone": {{"indexHost", "the index host"}},
}

// retrieveNodeIssues checks an ai_retrieve node's store and query.
func retrieveNodeIssues(n storage.WorkflowNode) (issues []ValidationIssue) {
	needs := func(what string) ValidationIssue {
		return ValidationIssue{
			Severity:       "error",
			Message:        fmt.Sprintf("AI retrieve node '%s' needs %s.", n.ID, what),
			Recommendation: "Open the node and fill in its vector store settings; until then every message fails at this node.",
			NodeID:         n.ID,
		}
	}
	store, _ := n.Config["store"].(string)
	keys, known := retrieveStoreKeys[strings.ToLower(strings.TrimSpace(store))]
	if !known {
		issues = append(issues, needs("a vector store (pgvector or pinecone)"))
	}
	for _, k := range keys {
		if isMissing(n.Config[k.key]) {
			issues = append(issues, needs(k.what))
		}
	}
	if isMissing(n.Config["query"]) && isMissing(n.Config["queryField"]) {
		issues = append(issues, needs("a query or a query field"))
	}
	return issues
}

// memoryIssue reports an ai_prompt node's conversation memory settings that
// do not parse: every message would fail at the node.
func memoryIssue(n storage.WorkflowNode, nodeType string) (ValidationIssue, bool) {
	if nodeType != "ai_prompt" {
		return ValidationIssue{}, false
	}
	if _, _, err := memory.Parse(n.Config[memory.ConfigKey]); err != nil {
		return ValidationIssue{
			Severity:       "error",
			Message:        fmt.Sprintf("AI node '%s' has invalid conversation memory settings: %v.", n.ID, err),
			Recommendation: `Use {"conversationField": "conversation_id", "maxTurns": 10, "ttl": "24h"}, or turn memory off.`,
			NodeID:         n.ID,
		}, true
	}
	return ValidationIssue{}, false
}

// isMissing reports a setting that is absent or blank text. A list or an
// object (labels, a schema given as JSON) counts as present.
func isMissing(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) == ""
}
