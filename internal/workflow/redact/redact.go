// Package redact removes credentials from a workflow before it leaves the
// instance it runs on.
package redact

import (
	"maps"
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
)

// Placeholder replaces a plaintext API key in an exported workflow. It is
// not a {{ }} template on purpose: a template would resolve to a secret of
// whatever name it gave in the importing vhost, or silently to nothing,
// whereas this fails the provider call loudly until someone sets the key.
const Placeholder = "[REDACTED]"

// aiKeyFields are the node settings that hold a model provider's API key:
// the primary connection's and the fallback connection's
// (pkg/comm/transformer/genai reads the fallback with a "fallback" prefix).
var aiKeyFields = []string{"apiKey", "fallbackApiKey"}

// AIKeys returns nodes with every plaintext API key on an AI node replaced
// by Placeholder. A key written as a {{ }} reference (a vhost secret) is a
// name, not a credential, and is kept. The nodes passed in are not modified;
// a node whose config changes gets its own copy of the map.
func AIKeys(nodes []storage.WorkflowNode) []storage.WorkflowNode {
	out := make([]storage.WorkflowNode, len(nodes))
	for i, n := range nodes {
		out[i] = n
		if !isAINode(n) {
			continue
		}
		var cfg map[string]any
		for _, field := range aiKeyFields {
			key, _ := n.Config[field].(string)
			if strings.TrimSpace(key) == "" || strings.Contains(key, "{{") {
				continue
			}
			if cfg == nil {
				cfg = maps.Clone(n.Config)
			}
			cfg[field] = Placeholder
		}
		if cfg != nil {
			out[i].Config = cfg
		}
	}
	return out
}

// isAINode reports a node that calls a language model. The editor stores a
// transformation's real type in transType; executors such as ai_classify use
// the node type itself. Every AI node type is named ai_*.
func isAINode(n storage.WorkflowNode) bool {
	t, _ := n.Config["transType"].(string)
	if t == "" {
		t = n.Type
	}
	return strings.HasPrefix(t, "ai_")
}
