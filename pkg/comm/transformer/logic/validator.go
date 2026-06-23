package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/user/hermod/pkg/comm/transformer"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("validator", &ValidatorTransformer{})
}

type ValidatorTransformer struct{}

func (t *ValidatorTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	schema, _ := config["schema"].(string)
	if schema == "" {
		return msg, nil
	}

	var rules map[string]string
	err := json.Unmarshal([]byte(schema), &rules)
	if err != nil {
		return nil, fmt.Errorf("failed to parse validation schema: %w", err)
	}

	for path, expectedType := range rules {
		val := evaluator.EvaluateField(msg, path)
		if val == nil {
			return nil, fmt.Errorf("validation failed: field %s is missing", path)
		}
		actualType := fmt.Sprintf("%T", val)
		if expectedType != "" && !strings.Contains(strings.ToLower(actualType), strings.ToLower(expectedType)) {
			return nil, fmt.Errorf("validation failed: field %s expected type %s, got %s", path, expectedType, actualType)
		}
	}

	return msg, nil
}
