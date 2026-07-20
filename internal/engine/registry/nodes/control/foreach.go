package control

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry/interfaces"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/infra/evaluator"
)

func init() {
	interfaces.RegisterNodeExecutor("foreach", &ForeachNode{})
}

// ForeachNode implements execution-level fan-out.
type ForeachNode struct{}

// Execute splits a single message into multiple messages based on an array field.
func (n *ForeachNode) Execute(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	arrayPath, _ := node.Config["arrayPath"].(string)
	if arrayPath == "" {
		return nil, "", errors.New("foreach: arrayPath is required")
	}

	raw := evaluator.GetMsgValByPath(msg, arrayPath)
	arr, ok := raw.([]any)
	if !ok {
		return nil, "", fmt.Errorf("foreach: value at %s is not an array", arrayPath)
	}

	if len(arr) == 0 {
		return nil, "", nil
	}

	results := make([]hermod.Message, 0, len(arr))
	for i, item := range arr {
		m := msg.Clone()
		m.SetData("_item", item)
		m.SetData("_index", i)
		// Correlation/idempotency metadata for downstream sinks and debugging
		m.SetMetadata("_fanout_group", msg.ID())
		m.SetMetadata("_fanout_index", strconv.Itoa(i))
		m.SetMetadata("_fanout_total", strconv.Itoa(len(arr)))
		results = append(results, m)
	}

	return results, "", nil
}
