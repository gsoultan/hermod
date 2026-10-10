package http

import (
	"fmt"
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
)

// aiNodeTypes are the nodes that call a language model, with the setting
// each cannot run without.
var aiNodeTypes = map[string]struct{ key, what string }{
	"ai_prompt":   {"prompt", "a prompt"},
	"ai_extract":  {"schema", "a JSON Schema"},
	"ai_classify": {"labels", "at least one label"},
	"ai_embed":    {"", ""},
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
		key := str("apiKey")
		if key != "" && !strings.Contains(key, "{{") && !keylessAIProviders[provider] {
			issues = append(issues, ValidationIssue{
				Severity: "warning",
				Message:  fmt.Sprintf("AI node '%s' stores its API key in the workflow.", n.ID),
				Recommendation: `Save the key as a vhost secret and set the node's API key to {{secret("NAME")}}. ` +
					"A key typed into the node is saved with the workflow and included in exports.",
				NodeID: n.ID,
			})
		}
	}
	return issues
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
