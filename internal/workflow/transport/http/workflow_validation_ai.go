package http

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"
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
		if issue, bad := apiKeyIssue(n.ID, str("apiKey"), provider); bad {
			issues = append(issues, issue)
		}
	}
	return issues
}

// apiKeyIssue warns about an AI node's API key that an export removed, or
// that is typed into the node instead of coming from a secret.
func apiKeyIssue(nodeID, key, provider string) (ValidationIssue, bool) {
	switch {
	case key == redact.Placeholder:
		return ValidationIssue{
			Severity:       "warning",
			Message:        fmt.Sprintf("AI node '%s' has no API key: it was removed when the workflow was exported.", nodeID),
			Recommendation: `Save the key as a vhost secret and set the node's API key to {{secret("NAME")}}; until then every call to the provider is refused.`,
			NodeID:         nodeID,
		}, true
	case key != "" && !strings.Contains(key, "{{") && !keylessAIProviders[provider]:
		return ValidationIssue{
			Severity: "warning",
			Message:  fmt.Sprintf("AI node '%s' stores its API key in the workflow.", nodeID),
			Recommendation: `Save the key as a vhost secret and set the node's API key to {{secret("NAME")}}. ` +
				"A key typed into the node is saved with the workflow and its version history (exports replace it with a placeholder).",
			NodeID: nodeID,
		}, true
	}
	return ValidationIssue{}, false
}

// agentNodeIssues checks an ai_agent node: it needs a goal and at least one
// tool, a sink tool must name a sink node of this workflow, an mcp tool its
// server and remote tool (see mcpToolIssues), and a write tool
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
		if kind == "mcp" {
			issues = append(issues, mcpToolIssues(n.ID, name, t)...)
		}
		// An mcp tool writes unless the workflow says write: false and its
		// server marks it read-only, which is only known at run time, so its
		// opt-out is called out too.
		write := kind == "sink" || kind == "mcp" || t["write"] == true
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

// mcpToolIssues checks an agent's mcp tool: it needs the server's url (http
// or https) and the remote tool's name, and a header that looks like a
// credential should come from a secret rather than be saved in the workflow.
func mcpToolIssues(nodeID, name string, t map[string]any) (issues []ValidationIssue) {
	needs := func(what string) ValidationIssue {
		return ValidationIssue{
			Severity:       "error",
			Message:        fmt.Sprintf("AI agent '%s' MCP tool '%s' needs %s.", nodeID, name, what),
			Recommendation: "Open the tool's settings and fill in the MCP server and the remote tool to call; until then every message fails at this node.",
			NodeID:         nodeID,
		}
	}
	server, _ := t["server"].(map[string]any)
	rawURL, _ := server["url"].(string)
	rawURL = strings.TrimSpace(rawURL)
	switch {
	case rawURL == "":
		issues = append(issues, needs("the server's url (server.url)"))
	case !strings.Contains(rawURL, "{{") && !httpURL(rawURL):
		issues = append(issues, needs("an http or https server url"))
	}
	if remote, _ := t["tool"].(string); strings.TrimSpace(remote) == "" {
		issues = append(issues, needs("the name of the remote tool it calls (tool)"))
	}
	headers, _ := server["headers"].(map[string]any)
	for _, header := range slices.Sorted(maps.Keys(headers)) {
		value, _ := headers[header].(string)
		if value == "" || strings.Contains(value, "{{") || !credentialHeader(header) {
			continue
		}
		issues = append(issues, ValidationIssue{
			Severity: "warning",
			Message:  fmt.Sprintf("AI agent '%s' MCP tool '%s' stores header '%s' in the workflow.", nodeID, name, header),
			Recommendation: `Save the value as a vhost secret and set the header to {{secret("NAME")}} (for example Bearer {{secret("NAME")}}). ` +
				"A credential typed into the node is saved with the workflow and its version history.",
			NodeID: nodeID,
		})
	}
	return issues
}

// credentialHeader reports whether a header name looks like it carries a
// credential.
func credentialHeader(name string) bool {
	n := strings.ToLower(name)
	switch n {
	case "authorization", "proxy-authorization", "cookie":
		return true
	}
	for _, part := range []string{"key", "token", "secret", "password"} {
		if strings.Contains(n, part) {
			return true
		}
	}
	return false
}

func httpURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
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
