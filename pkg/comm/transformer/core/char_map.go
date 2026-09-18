package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod/pkg/comm/transformer"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("char_map", &CharMapTransformer{})
}

type CharMapTransformer struct{}

func (t *CharMapTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	field, _ := config["field"].(string)
	if field == "" {
		return msg, nil
	}

	// Resolved in a fixed order -- the list, then `operation`, then the `op` the
	// editor writes -- so a config carrying more than one of them behaves the
	// same way every time rather than depending on which key was written last.
	//
	// `op` is read here because the editor's Operation select has always written
	// that key (ui/src/components/workflow/Transformation/configs/data/CharMapConfig.tsx)
	// while this only ever looked for the other two. The operation list came out
	// empty, the loop below did nothing, and the node wrote its field back
	// untouched: a green node, no error and nothing in the logs. The editor is
	// the only way to build a Character Map node, so that was every one of them.
	ops, _ := config["operations"].([]any)
	if len(ops) == 0 {
		for _, key := range []string{"operation", "op"} {
			if s, ok := config[key].(string); ok && s != "" {
				ops = append(ops, s)
				break
			}
		}
	}

	valRaw := evaluator.GetMsgValByPath(msg, field)
	if valRaw == nil {
		return msg, nil
	}
	val := fmt.Sprintf("%v", valRaw)

	for _, opRaw := range ops {
		op, ok := opRaw.(string)
		if !ok {
			continue
		}

		switch strings.ToLower(op) {
		case "uppercase":
			val = strings.ToUpper(val)
		case "lowercase":
			val = strings.ToLower(val)
		case "trim":
			val = strings.TrimSpace(val)
		case "trim_left":
			val = strings.TrimLeft(val, " \t\n\r")
		case "trim_right":
			val = strings.TrimRight(val, " \t\n\r")
		}
	}

	targetField, _ := config["targetField"].(string)
	if targetField == "" {
		targetField = field
	}

	msg.SetData(targetField, val)
	return msg, nil
}
