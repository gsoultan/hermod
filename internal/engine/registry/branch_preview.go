package registry

import (
	"context"
	"fmt"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
)

// PreviewBranch runs a routing node against a sample message and reports the
// branch it would take.
//
// The editor's "Test" button used to have one path: the transformer registry.
// That covers every node whose job is to change a message, and none of the
// nodes whose job is to choose where it goes. switch, condition and router are
// registered as node executors and dispatched at runtime by
// executeNodeInternal, so a workflow containing them ran correctly while the
// preview of the same node answered
//
//	unknown transformation type "switch": no transformer is registered under that name
//
// which reads as a broken configuration rather than a preview that never looked
// in the right registry.
//
// Only executors that declare themselves interfaces.PreviewSafe run here. A
// node that exists but has not declared it is refused by name, because a
// preview is not a reason to write to a sink or record an approval.
func (r *Registry) PreviewBranch(ctx context.Context, nodeType string, config map[string]any, msg hermod.Message) ([]hermod.Message, string, error) {
	executor, ok := interfaces.GetPreviewSafeExecutor(nodeType)
	if !ok {
		if _, exists := interfaces.GetNodeExecutor(nodeType); exists {
			return nil, "", fmt.Errorf("%q is a workflow node that cannot be previewed: "+
				"it does more than decide a route, so running it here would have "+
				"effects outside this preview", nodeType)
		}
		return nil, "", fmt.Errorf("%q is not a routing node", nodeType)
	}

	node := &storage.WorkflowNode{Type: nodeType, Config: config}
	return executor.Execute(r.ContextWithPipelineSnapshot(ctx), r, "", node, msg)
}

// IsBranchPreviewable reports whether nodeType names a routing node the editor
// can preview, so a caller can pick the right path before doing any work rather
// than reacting to an error afterwards.
func (r *Registry) IsBranchPreviewable(nodeType string) bool {
	_, ok := interfaces.GetPreviewSafeExecutor(nodeType)
	return ok
}

// CanTransform reports whether transType names a registered transformer.
//
// Some names are both: "validator" is a transformer and a node executor, and
// the transformer is the one a preview wants. Callers deciding how to preview a
// node need to ask this first, or a node that transforms perfectly well gets
// turned away for being a node.
func (r *Registry) CanTransform(transType string) bool {
	_, ok := transformer.Get(transType)
	return ok
}

// IsWorkflowNode reports whether nodeType names a registered node executor.
//
// Used to tell two failures apart. "stateful" is a real node that a preview
// cannot run, because running it would mutate state a live workflow depends on;
// "statefull" is a typo. Both used to produce
//
//	unknown transformation type: no transformer is registered under that name
//
// which describes the typo and misdescribes the node.
func (r *Registry) IsWorkflowNode(nodeType string) bool {
	_, ok := interfaces.GetNodeExecutor(nodeType)
	return ok
}
