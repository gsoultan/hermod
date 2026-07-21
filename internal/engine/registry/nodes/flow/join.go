package flow

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry/interfaces"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/infra/evaluator"
)

type JoinExecutor struct {
	mu [256]sync.Mutex
}

func (e *JoinExecutor) getMu(key string) *sync.Mutex {
	h := fnv.New32a()
	h.Write([]byte(key))
	return &e.mu[h.Sum32()%256]
}

func init() {
	interfaces.RegisterNodeExecutor("join", &JoinExecutor{})
}

func (e *JoinExecutor) Execute(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
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

func (e *JoinExecutor) handleJoin(nctx interfaces.NodeContext, nodeID, key string, msg hermod.Message, expected int) ([]hermod.Message, string, error) {
	stateKey := "join_" + nodeID + "_" + key
	mu := e.getMu(stateKey)
	mu.Lock()
	defer mu.Unlock()

	msgs := []hermod.Message{}
	if val, ok := nctx.GetNodeState(stateKey); ok {
		msgs = val.([]hermod.Message)
	}

	msg.Retain() // Retain for the join state
	msgs = append(msgs, msg)
	if len(msgs) < expected {
		nctx.SetNodeState(stateKey, msgs)
		return nil, "waiting", nil
	}

	nctx.SetNodeState(stateKey, nil) // Clear state
	merged := e.mergeMessages(msgs)

	// Release all messages held in the join state
	for _, m := range msgs {
		m.Release()
	}

	return []hermod.Message{merged}, "success", nil
}

func (e *JoinExecutor) mergeMessages(msgs []hermod.Message) hermod.Message {
	if len(msgs) == 0 {
		return nil
	}
	// Clone the first message to avoid modifying it in-place
	result := msgs[0].Clone()
	// Basic merge of data maps from other messages
	for i := 1; i < len(msgs); i++ {
		for k, v := range msgs[i].Data() {
			result.SetData(k, v)
		}
		for k, v := range msgs[i].Metadata() {
			result.SetMetadata(k, v)
		}
	}
	return result
}
