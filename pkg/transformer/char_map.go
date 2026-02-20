package transformer

import (
	"context"
	"fmt"
	"strings"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
)

func init() {
	Register("char_map", &CharMapTransformer{})
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

	ops, _ := config["operations"].([]any)
	if len(ops) == 0 {
		// Fallback to single operation if provided
		if op, ok := config["operation"].(string); ok {
			ops = append(ops, op)
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
