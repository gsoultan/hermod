package flow

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/infra/evaluator"
)

type JoinExecutor struct {
	mu sync.Mutex
}

func init() {
	registry.RegisterNodeExecutor("join", &JoinExecutor{})
}

func (e *JoinExecutor) Execute(ctx context.Context, nctx registry.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	keyPath, _ := node.Config["key_path"].(string)
	if keyPath == "" {
		return nil, "error", errors.New("join node requires key_path")
	}

	key := fmt.Sprintf("%v", evaluator.GetMsgValByPath(msg, keyPath))
	expected, _ := node.Config["expected_sources"].(float64)
	if expected == 0 {
		expected = 2
	}

	return e.handleJoin(nctx, node.ID, key, msg, int(expected))
}

func (e *JoinExecutor) handleJoin(nctx registry.NodeContext, nodeID, key string, msg hermod.Message, expected int) ([]hermod.Message, string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	stateKey := "join_" + nodeID + "_" + key
	msgs := []hermod.Message{}
	if val, ok := nctx.GetNodeState(stateKey); ok {
		msgs = val.([]hermod.Message)
	}

	msgs = append(msgs, msg)
	if len(msgs) < expected {
		nctx.SetNodeState(stateKey, msgs)
		return nil, "waiting", nil
	}

	nctx.SetNodeState(stateKey, nil) // Clear state
	merged := e.mergeMessages(msgs)
	return []hermod.Message{merged}, "success", nil
}

func (e *JoinExecutor) mergeMessages(msgs []hermod.Message) hermod.Message {
	result := msgs[0]
	// Basic merge of data maps
	for i := 1; i < len(msgs); i++ {
		for k, v := range msgs[i].Data() {
			result.SetData(k, v)
		}
	}
	return result
}
