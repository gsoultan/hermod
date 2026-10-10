// Package mcpserver exposes Hermod workflows to Model Context Protocol
// clients: an MCP client (Claude, ChatGPT, an IDE agent) can list the
// workflows a user may see, read their status, and run the ones their owner
// has opted in.
//
// The protocol binding lives in transport/http; this package holds the rules:
// which workflows are exposed, who may run them, and how a run is delivered
// and answered.
package mcpserver

import (
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
)

// ExposeTag is the workflow tag that opts a workflow into MCP. A workflow
// without it is invisible to MCP clients, whoever asks: exposing an
// automation to a model is a decision its owner makes, not a default.
//
// It is a tag rather than a new column so that it needs no migration and is
// edited where tags already are, in the workflow settings.
const ExposeTag = "mcp"

// IsExposed reports whether wf carries ExposeTag (any case, surrounding space
// ignored).
func IsExposed(wf storage.Workflow) bool {
	for _, t := range wf.Tags {
		if strings.EqualFold(strings.TrimSpace(t), ExposeTag) {
			return true
		}
	}
	return false
}
