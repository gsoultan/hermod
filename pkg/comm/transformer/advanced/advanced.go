package advanced

import (
	"context"
	"strings"

	"github.com/user/hermod/pkg/comm/transformer"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/infra/evaluator"
)

func init() {
	adv := &AdvancedTransformer{evaluator: evaluator.NewEvaluator()}
	transformer.Register("advanced", adv)
	transformer.Register("set", adv)
}

type columnConfig struct {
	path string
	expr any
}

type AdvancedTransformer struct {
	evaluator *evaluator.Evaluator
}

func (t *AdvancedTransformer) Prepare(config map[string]any) (map[string]any, error) {
	var columns []columnConfig
	for k, v := range config {
		if strings.HasPrefix(k, "column.") {
			columns = append(columns, columnConfig{
				path: strings.TrimPrefix(k, "column."),
				expr: v,
			})
		}
	}
	if len(columns) > 0 {
		config["_parsed_columns"] = columns
	}
	return config, nil
}

func (t *AdvancedTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	transType, _ := config["transType"].(string)

	var columns []columnConfig
	if cached, ok := config["_parsed_columns"].([]columnConfig); ok {
		columns = cached
	} else {
		// Fallback for non-prepared config
		for k, v := range config {
			if strings.HasPrefix(k, "column.") {
				columns = append(columns, columnConfig{
					path: strings.TrimPrefix(k, "column."),
					expr: v,
				})
			}
		}
	}

	if transType == "advanced" {
		results := make(map[string]any)
		for _, col := range columns {
			result := t.evaluator.EvaluateAdvancedExpression(msg, col.expr)
			if result != nil {
				results[col.path] = result
			}
		}

		msg.ClearPayloads()
		for colPath, result := range results {
			msg.SetData(colPath, result)
		}
	} else { // "set"
		for _, col := range columns {
			result := t.evaluator.EvaluateAdvancedExpression(msg, col.expr)
			msg.SetData(col.path, result)
		}
	}

	return msg, nil
}
