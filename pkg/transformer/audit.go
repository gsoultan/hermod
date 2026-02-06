package transformer

import (
	"context"
	"os"
	"time"

	"github.com/user/hermod"
)

func init() {
	Register("audit", &AuditTransformer{})
}

type AuditTransformer struct{}

func (t *AuditTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]interface{}) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	workflowID, _ := ctx.Value("workflow_id").(string)
	nodeID, _ := ctx.Value("node_id").(string)
	hostname, _ := os.Hostname()

	prefix, _ := config["prefix"].(string)
	if prefix == "" {
		prefix = "audit_"
	}

	msg.SetData(prefix+"workflow_id", workflowID)
	msg.SetData(prefix+"node_id", nodeID)
	msg.SetData(prefix+"machine_name", hostname)
	msg.SetData(prefix+"timestamp", time.Now().Format(time.RFC3339))
	msg.SetData(prefix+"message_id", msg.ID())

	return msg, nil
}
