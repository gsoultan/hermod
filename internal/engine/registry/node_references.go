package registry

import (
	"context"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/security/secrets"
)

// skipNodeSetting reports a node setting whose secret and env references must
// not be expanded into its text before the node runs:
//
//   - a SQL template, where every {{ }} token becomes a bound parameter.
//     Splicing a secret into the statement would turn a value into SQL;
//   - a pipeline's steps, which are expanded one step at a time as each runs,
//     so the rule above holds inside them too;
//   - the engine's own caches, written under a leading underscore.
func skipNodeSetting(key string) bool {
	if strings.HasPrefix(key, "_") {
		return true
	}
	switch key {
	case "queryTemplate", "whereClause", "query", "queries", "steps":
		return true
	}
	return false
}

// messageVHost is the vhost the engine marked msg with -- never its data, so
// a payload cannot choose whose secrets its node reads.
func messageVHost(msg hermod.Message) string {
	if scoped, ok := msg.(hermod.VHostScoped); ok && scoped != nil {
		return scoped.VHost()
	}
	return ""
}

// nodeSecrets is the lookup a node's settings resolve through: the cache the
// expression functions read, since this runs once per message.
func (r *Registry) nodeSecrets() secrets.ScopedManager {
	r.secretMu.RLock()
	cache := r.expressionSecrets
	r.secretMu.RUnlock()
	if cache != nil {
		return cache
	}
	return r.vhostSecrets()
}

// expandNodeConfig resolves the secret and env references in a node's
// settings for the vhost msg runs in -- {{secret:NAME}}, {{secret("NAME")}},
// {{env("NAME")}} and the rest secrets.ExpandTokenReferences reads -- in every
// setting, at any depth, except those skipNodeSetting names. Every other token
// is left for the node to resolve per message, so record data still cannot
// choose a credential or a destination.
//
// The config passed in is returned as it is when it holds no reference, and
// is never modified: a copy carries what changed.
func (r *Registry) expandNodeConfig(ctx context.Context, msg hermod.Message, config map[string]any) (map[string]any, bool, error) {
	if len(config) == 0 {
		return config, false, nil
	}
	out, changed, err := secrets.ExpandSettings(ctx, r.nodeSecrets(), messageVHost(msg), config, skipNodeSetting)
	if err != nil {
		return nil, false, fmt.Errorf("resolving node settings: %w", err)
	}
	return out, changed, nil
}

// withExpandedConfig is node with its settings expanded, as a copy when any
// changed. A transformation is left alone: its settings are expanded where the
// transformation runs, one pipeline step at a time.
func (r *Registry) withExpandedConfig(ctx context.Context, node *storage.WorkflowNode, msg hermod.Message) (*storage.WorkflowNode, error) {
	switch node.Type {
	case "transformation", "source", "sink":
		return node, nil
	}
	cfg, changed, err := r.expandNodeConfig(ctx, msg, node.Config)
	if err != nil {
		return nil, fmt.Errorf("node %s: %w", node.ID, err)
	}
	if !changed {
		return node, nil
	}
	copied := *node
	copied.Config = cfg
	return &copied, nil
}
