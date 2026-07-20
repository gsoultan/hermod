package control

import (
	"context"
	"fmt"
	"strconv"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry/interfaces"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/infra/evaluator"
)

func init() {
	interfaces.RegisterNodeExecutor("stateful", &StatefulNode{})
}

// StatefulNode handles stateful operations like counting or summing.
type StatefulNode struct{}

// Execute performs the stateful operation and updates message data.
func (n *StatefulNode) Execute(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	op, _ := node.Config["operation"].(string)
	field, _ := node.Config["field"].(string)
	outputField, _ := node.Config["outputField"].(string)
	if outputField == "" {
		outputField = field + "_" + op
	}

	currentVal := n.getCurrentValue(ctx, nctx, workflowID, node)

	switch op {
	case "count":
		currentVal++
	case "sum":
		val := evaluator.GetMsgValByPath(msg, field)
		if v, ok := evaluator.ToFloat64(val); ok {
			currentVal += v
		}
	}

	n.saveValue(ctx, nctx, workflowID, node, currentVal)

	modifiedMsg := msg.Clone()
	modifiedMsg.SetData(outputField, currentVal)
	return []hermod.Message{modifiedMsg}, "", nil
}

func (n *StatefulNode) getCurrentValue(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode) float64 {
	key := workflowID + ":" + node.ID
	if store := nctx.StateStore(); store != nil {
		if valBytes, err := store.Get(ctx, "node:"+key); err == nil && valBytes != nil {
			v, _ := strconv.ParseFloat(string(valBytes), 64)
			return v
		}
	}

	if state, ok := nctx.GetNodeState(key); ok {
		if v, ok := state.(float64); ok {
			return v
		}
	}
	return 0
}

func (n *StatefulNode) saveValue(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, val float64) {
	key := workflowID + ":" + node.ID
	if store := nctx.StateStore(); store != nil {
		_ = store.Set(ctx, "node:"+key, []byte(fmt.Sprintf("%f", val)))
	} else {
		nctx.SetNodeState(key, val)
	}
}
