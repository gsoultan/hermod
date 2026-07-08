package core

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/user/hermod/pkg/comm/transformer"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("mapping", &MappingTransformer{})
}

type MappingTransformer struct{}

func (t *MappingTransformer) Prepare(config map[string]any) (map[string]any, error) {
	mappingStr, _ := config["mapping"].(string)
	if mappingStr != "" {
		var mapping map[string]any
		if err := json.Unmarshal([]byte(mappingStr), &mapping); err == nil {
			config["_parsed_mapping"] = mapping
		}
	}
	return config, nil
}

func (t *MappingTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	field, _ := config["field"].(string)
	var mapping map[string]any
	if cached, ok := config["_parsed_mapping"].(map[string]any); ok {
		mapping = cached
	} else {
		mappingStr, _ := config["mapping"].(string)
		_ = json.Unmarshal([]byte(mappingStr), &mapping)
	}

	mappingType, _ := config["mappingType"].(string) // "exact", "range", "regex"

	fieldValRaw := evaluator.EvaluateField(msg, field)
	fieldVal := fmt.Sprintf("%v", fieldValRaw)

	switch mappingType {
	case "range":
		val, ok := evaluator.ToFloat64(fieldValRaw)
		if ok {
			for k, v := range mapping {
				if strings.Contains(k, "-") {
					parts := strings.Split(k, "-")
					if len(parts) == 2 {
						low, _ := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
						high, _ := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
						if val >= low && val <= high {
							msg.SetData(field, v)
							return msg, nil
						}
					}
				} else if strings.HasSuffix(k, "+") {
					low, _ := strconv.ParseFloat(strings.TrimSuffix(k, "+"), 64)
					if val >= low {
						msg.SetData(field, v)
						return msg, nil
					}
				}
			}
		}
	case "regex":
		for k, v := range mapping {
			matched, _ := regexp.MatchString(k, fieldVal)
			if matched {
				msg.SetData(field, v)
				return msg, nil
			}
		}
	default:
		// exact (default)
		if newVal, ok := mapping[fieldVal]; ok {
			msg.SetData(field, newVal)
		}
	}
	return msg, nil
}
