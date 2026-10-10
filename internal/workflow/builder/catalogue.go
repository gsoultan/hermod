// Package builder drafts a workflow from a plain-language description with a
// language model. The draft is a proposal for a person to review in the
// editor: it is validated and returned, never saved and never started.
package builder

import (
	"slices"

	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/pkg/comm/transformer"

	// The catalogue is read from the node and transformer registries, which
	// are filled by these packages' init functions. Imported here so the
	// catalogue is complete wherever the builder is linked, not only in the
	// binary whose main happens to import them.
	_ "github.com/gsoultan/hermod/internal/engine/registry/nodes"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/advanced"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/ai"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/core"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/genai"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/genai/retrieve"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/logic"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/lookup"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/ml"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/security"
)

// Kind is one kind of node a draft may contain.
type Kind struct {
	// Key names the kind in the prompt: the node type for a node executor,
	// "transformation:<transType>" for a transformer.
	Key string
	// NodeType is the workflow node's type.
	NodeType string
	// TransType is config.transType for a transformation node.
	TransType string
	// Description tells the model what the node does and how it is set up.
	Description string
}

// Structural node types: they have no executor because the engine handles
// them itself, and every workflow needs them.
const (
	TypeSource         = "source"
	TypeSink           = "sink"
	TypeTransformation = "transformation"
)

// Catalogue lists every node kind a draft may use, read from what is
// registered in this binary. A kind is offered only if it can run, so the
// model cannot be told about a node that does not exist.
func Catalogue() []Kind {
	kinds := []Kind{{Key: TypeSource, NodeType: TypeSource, Description: descriptions[TypeSource]}}
	for _, t := range interfaces.NodeExecutorTypes() {
		if t == TypeTransformation {
			continue // the dispatcher for the transformer kinds below
		}
		kinds = append(kinds, Kind{Key: t, NodeType: t, Description: descriptions[t]})
	}
	if !slices.ContainsFunc(kinds, func(k Kind) bool { return k.Key == TypeSink }) {
		kinds = append(kinds, Kind{Key: TypeSink, NodeType: TypeSink, Description: descriptions[TypeSink]})
	}
	for _, name := range transformer.Names() {
		key := TypeTransformation + ":" + name
		kinds = append(kinds, Kind{Key: key, NodeType: TypeTransformation, TransType: name, Description: descriptions[key]})
	}
	return kinds
}

// nodeTypes lists the node types a draft may use.
func nodeTypes(kinds []Kind) []string {
	var out []string
	for _, k := range kinds {
		if !slices.Contains(out, k.NodeType) {
			out = append(out, k.NodeType)
		}
	}
	slices.Sort(out)
	return out
}
