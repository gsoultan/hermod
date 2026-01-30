package transformer

import (
	"context"
	"strings"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
)

func init() {
	adv := &AdvancedTransformer{evaluator: evaluator.NewEvaluator()}
	Register("advanced", adv)
	Register("set", adv)
}

type AdvancedTransformer struct {
	evaluator *evaluator.Evaluator
}

func (t *AdvancedTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]interface{}) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	transType, _ := config["transType"].(string)

	if transType == "advanced" {
		results := make(map[string]interface{})
		for k, v := range config {
			if !strings.HasPrefix(k, "column.") {
				continue
			}
			colPath := strings.TrimPrefix(k, "column.")
			result := t.evaluator.EvaluateAdvancedExpression(msg, v)
			if result != nil {
				results[colPath] = result
			}
		}

		msg.ClearPayloads()
		for colPath, result := range results {
			msg.SetData(colPath, result)
		}
	} else { // "set"
		for k, v := range config {
			if strings.HasPrefix(k, "column.") {
				colPath := strings.TrimPrefix(k, "column.")
				result := t.evaluator.EvaluateAdvancedExpression(msg, v)
				msg.SetData(colPath, result)
			}
		}
	}

	return msg, nil
}
