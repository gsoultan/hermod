package transformer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/user/hermod"
)

func init() {
	Register("validator", &ValidatorTransformer{})
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
		val := getMsgValByPath(msg, path)
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

func getMsgValByPath(msg hermod.Message, path string) any {
	if path == "" {
		return nil
	}
	parts := strings.Split(path, ".")
	var current any = msg.Data()

	for _, part := range parts {
		if m, ok := current.(map[string]any); ok {
			current = m[part]
		} else {
			return nil
		}
	}
	return current
}
