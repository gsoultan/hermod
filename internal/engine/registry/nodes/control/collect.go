package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/storage"
)

func init() {
	registry.RegisterNodeExecutor("collect", &CollectNode{})
}

// CollectNode (Fan-in) waits for all messages from a fan-out group before continuing.
type CollectNode struct {
	mu sync.Mutex // Local lock for state store access if needed, though state store should handle concurrency
}

// Execute accumulates messages until all items of a fan-out group are received.
func (n *CollectNode) Execute(ctx context.Context, nctx registry.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	groupID := msg.Metadata()["_fanout_group"]
	totalStr := msg.Metadata()["_fanout_total"]

	if groupID == "" || totalStr == "" {
		return []hermod.Message{msg}, "", nil
	}

	total := 0
	fmt.Sscanf(totalStr, "%d", &total)
	if total <= 1 {
		return []hermod.Message{msg}, "", nil
	}

	return n.handleCollection(ctx, nctx, workflowID, node, msg, groupID, total)
}

func (n *CollectNode) handleCollection(ctx context.Context, nctx registry.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message, groupID string, total int) ([]hermod.Message, string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	key := fmt.Sprintf("collect:%s:%s:%s", workflowID, node.ID, groupID)
	store := nctx.StateStore()
	if store == nil {
		return nil, "", errors.New("collect: state store not available")
	}

	items := n.loadItems(ctx, store, key)
	items = append(items, n.getMessageItem(msg))

	if len(items) >= total {
		return n.finish(ctx, nctx, store, workflowID, node, msg, key, items, groupID)
	}

	return n.persist(ctx, nctx, store, workflowID, key, items, groupID, total, msg.ID())
}

func (n *CollectNode) loadItems(ctx context.Context, store hermod.StateStore, key string) []any {
	data, err := store.Get(ctx, key)
	var items []any
	if err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &items)
	}
	return items
}

func (n *CollectNode) getMessageItem(msg hermod.Message) any {
	if item := msg.Data()["_item"]; item != nil {
		return item
	}
	return msg.Data()
}

func (n *CollectNode) finish(ctx context.Context, nctx registry.NodeContext, store hermod.StateStore, workflowID string, node *storage.WorkflowNode, msg hermod.Message, key string, items []any, groupID string) ([]hermod.Message, string, error) {
	targetField, _ := node.Config["targetField"].(string)
	if targetField == "" {
		targetField = "_items"
	}

	resMsg := msg.Clone()
	resMsg.SetData(targetField, items)
	resMsg.SetData("_count", len(items))

	_ = store.Delete(ctx, key)
	nctx.BroadcastLog(workflowID, "INFO", fmt.Sprintf("Collect complete for group %s (%d items)", groupID, len(items)), msg.ID())
	return []hermod.Message{resMsg}, "", nil
}

func (n *CollectNode) persist(ctx context.Context, nctx registry.NodeContext, store hermod.StateStore, workflowID string, key string, items []any, groupID string, total int, msgID string) ([]hermod.Message, string, error) {
	newData, _ := json.Marshal(items)
	_ = store.Set(ctx, key, newData)

	nctx.BroadcastLog(workflowID, "INFO", fmt.Sprintf("Collected %d/%d items for group %s", len(items), total, groupID), msgID)
	return nil, "", nil
}
