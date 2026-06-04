package util

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/infra/evaluator"
)

func init() {
	registry.RegisterNodeExecutor("log", &LogNode{})
}

// LogNode explicitly broadcasts a log message with data for UI visibility.
type LogNode struct{}

// Execute logs the message or specific fields and continues.
func (n *LogNode) Execute(ctx context.Context, nctx registry.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	level, _ := node.Config["level"].(string)
	if level == "" {
		level = "INFO"
	}

	messageTmpl, _ := node.Config["message"].(string)
	if messageTmpl == "" {
		messageTmpl = "Log node triggered"
	}

	path, _ := node.Config["path"].(string)

	var dataLog string
	if path != "" {
		val := evaluator.GetMsgValByPath(msg, path)
		jb, _ := json.MarshalIndent(val, "", "  ")
		dataLog = string(jb)
	} else {
		jb, _ := json.MarshalIndent(msg.Data(), "", "  ")
		dataLog = string(jb)
	}

	nctx.BroadcastLog(workflowID, level, fmt.Sprintf("%s\n%s", messageTmpl, dataLog), msg.ID())

	return []hermod.Message{msg}, "", nil
}
