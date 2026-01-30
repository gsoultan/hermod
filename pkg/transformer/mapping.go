package transformer

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
)

func init() {
	Register("mapping", &MappingTransformer{})
}

type MappingTransformer struct{}

func (t *MappingTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]interface{}) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	field, _ := config["field"].(string)
	mappingStr, _ := config["mapping"].(string)
	var mapping map[string]interface{}
	_ = json.Unmarshal([]byte(mappingStr), &mapping)

	mappingType, _ := config["mappingType"].(string) // "exact", "range", "regex"

	fieldValRaw := evaluator.GetMsgValByPath(msg, field)
	fieldVal := fmt.Sprintf("%v", fieldValRaw)

	if mappingType == "range" {
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
	} else if mappingType == "regex" {
		for k, v := range mapping {
			matched, _ := regexp.MatchString(k, fieldVal)
			if matched {
				msg.SetData(field, v)
				return msg, nil
			}
		}
	} else {
		// exact (default)
		if newVal, ok := mapping[fieldVal]; ok {
			msg.SetData(field, newVal)
		}
	}
	return msg, nil
}
