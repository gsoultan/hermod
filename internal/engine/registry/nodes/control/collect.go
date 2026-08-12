package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry/interfaces"
	"github.com/user/hermod/internal/storage"
)

func init() {
	interfaces.RegisterNodeExecutor("collect", &CollectNode{})
}

// CollectNode (Fan-in) waits for all messages from a fan-out group before continuing.
type CollectNode struct {
	mu sync.Mutex // Local lock for state store access if needed, though state store should handle concurrency
}

// Execute accumulates messages until all items of a fan-out group are received.
func (n *CollectNode) Execute(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
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

func (n *CollectNode) handleCollection(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message, groupID string, total int) ([]hermod.Message, string, error) {
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

func (n *CollectNode) finish(ctx context.Context, nctx interfaces.NodeContext, store hermod.StateStore, workflowID string, node *storage.WorkflowNode, msg hermod.Message, key string, items []any, groupID string) ([]hermod.Message, string, error) {
	targetField, _ := node.Config["targetField"].(string)
	if targetField == "" {
		targetField = "_items"
	}

	resMsg := msg.Clone()
	resMsg.SetData(targetField, items)
	resMsg.SetData("_count", len(items))

	// The batch is complete and is emitted below, so a failed cleanup must not
	// fail it — that would throw away work that succeeded. It does have to be
	// loud: the items stay behind, and the next group with this id starts
	// part-full and emits a batch carrying another group's data.
	if err := store.Delete(ctx, key); err != nil {
		nctx.BroadcastLog(workflowID, "ERROR", fmt.Sprintf(
			"Collect emitted group %s but could not clear its state (%v); the next group "+
				"with this id will start with these %d items still in it", groupID, err, len(items)), msg.ID())
	}
	nctx.BroadcastLog(workflowID, "INFO", fmt.Sprintf("Collect complete for group %s (%d items)", groupID, len(items)), msg.ID())
	return []hermod.Message{resMsg}, "", nil
}

func (n *CollectNode) persist(ctx context.Context, nctx interfaces.NodeContext, store hermod.StateStore, workflowID string, key string, items []any, groupID string, total int, msgID string) ([]hermod.Message, string, error) {
	newData, err := json.Marshal(items)
	if err != nil {
		return nil, "", fmt.Errorf("collect: could not encode the group so far: %w", err)
	}

	// This returns no messages: the item is accumulated in the state store and
	// the group emits once it is complete. An item that was not stored is not
	// merely one lost message — the group never reaches its total, so every
	// message already collected into it is lost with it.
	if err := store.Set(ctx, key, newData); err != nil {
		return nil, "", fmt.Errorf("collect: could not store item %d/%d for group %s, "+
			"so the group would never complete: %w", len(items), total, groupID, err)
	}

	nctx.BroadcastLog(workflowID, "INFO", fmt.Sprintf("Collected %d/%d items for group %s", len(items), total, groupID), msgID)
	return nil, "", nil
}
