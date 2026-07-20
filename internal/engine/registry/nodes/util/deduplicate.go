package util

import (
	"context"
	"fmt"
	"sync"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry/interfaces"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/infra/evaluator"
	"github.com/user/hermod/pkg/infra/filter"
)

func init() {
	interfaces.RegisterNodeExecutor("deduplicate", &DeduplicateNode{
		filters: make(map[string]filter.Filter),
	})
}

// DeduplicateNode handles high-speed, in-memory deduplication using Bloom Filters.
type DeduplicateNode struct {
	mu      sync.Mutex
	filters map[string]filter.Filter
}

// Execute checks if the message is a duplicate based on a configured key.
func (n *DeduplicateNode) Execute(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	key := n.extractKey(node, msg)
	if key == "" {
		return []hermod.Message{msg}, "", nil
	}

	f := n.getFilter(workflowID, node.ID)
	if f.Test([]byte(key)) {
		nctx.BroadcastLog(workflowID, "INFO", "Duplicate detected for key: "+key, msg.ID())
		return nil, "duplicate", nil
	}

	f.Add([]byte(key))
	return []hermod.Message{msg}, "", nil
}

func (n *DeduplicateNode) extractKey(node *storage.WorkflowNode, msg hermod.Message) string {
	path, _ := node.Config["keyPath"].(string)
	if path == "" {
		path = "id"
	}
	val := evaluator.GetMsgValByPath(msg, path)
	if val == nil {
		return ""
	}
	return fmt.Sprintf("%v", val)
}

func (n *DeduplicateNode) getFilter(workflowID, nodeID string) filter.Filter {
	n.mu.Lock()
	defer n.mu.Unlock()

	id := workflowID + ":" + nodeID
	if f, ok := n.filters[id]; ok {
		return f
	}

	// Default: 100k items per rotation, approx 180KB per Bloom Filter.
	// Using a rotating filter to prevent false positives from increasing over time.
	f := filter.NewRotatingBloomFilter(100000*14, 10, 100000)
	n.filters[id] = f
	return f
}
