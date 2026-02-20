package transformer

import (
	"context"
	"encoding/json"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
)

func init() {
	f := &FilterTransformer{}
	Register("filter_data", f)
	Register("validate", f)
}

type FilterTransformer struct{}

func (t *FilterTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	transType, _ := config["transType"].(string)

	conditionsStr, _ := config["conditions"].(string)
	var conditions []map[string]any
	if conditionsStr != "" {
		_ = json.Unmarshal([]byte(conditionsStr), &conditions)
	}

	// Fallback to old format if no conditions array
	if len(conditions) == 0 {
		field, _ := config["field"].(string)
		op, _ := config["operator"].(string)
		val, _ := config["value"].(string)
		if field != "" {
			conditions = append(conditions, map[string]any{
				"field":    field,
				"operator": op,
				"value":    val,
			})
		}
	}

	isValid := evaluator.EvaluateConditions(msg, conditions)
	asField := evaluator.ToBool(config["asField"]) || transType == "validate"

	if asField {
		targetField, _ := config["targetField"].(string)
		if targetField == "" {
			targetField = "is_valid"
		}
		msg.SetData(targetField, isValid)
		return msg, nil
	}

	if isValid {
		return msg, nil
	}
	return nil, nil // Filtered
}
