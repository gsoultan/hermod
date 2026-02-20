package transformer

import (
	"context"
	"fmt"
	"strconv"

	"github.com/user/hermod"
)

func init() {
	Register("row_count", &RowCountTransformer{})
}

type RowCountTransformer struct{}

func (t *RowCountTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	variableName, _ := config["variableName"].(string)
	if variableName == "" {
		variableName = "row_count"
	}

	workflowID, _ := ctx.Value("workflow_id").(string)
	nodeID, _ := ctx.Value("node_id").(string)
	stateKey := fmt.Sprintf("rowcount:%s:%s:%s", workflowID, nodeID, variableName)

	var store hermod.StateStore
	if s, ok := ctx.Value(hermod.StateStoreKey).(hermod.StateStore); ok {
		store = s
	}

	if store != nil {
		count := int64(0)
		data, err := store.Get(ctx, stateKey)
		if err == nil && data != nil {
			count, _ = strconv.ParseInt(string(data), 10, 64)
		}
		count++
		_ = store.Set(ctx, stateKey, []byte(strconv.FormatInt(count, 10)))

		// Also inject into message if requested
		if asField, _ := config["asField"].(bool); asField {
			msg.SetData(variableName, count)
		}
	}

	return msg, nil
}
