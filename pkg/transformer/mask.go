package transformer

import (
	"context"
	"fmt"
	"strings"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
)

func init() {
	Register("mask", &MaskTransformer{})
}

type MaskTransformer struct{}

func (t *MaskTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]interface{}) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	field, _ := config["field"].(string)
	maskType, _ := config["maskType"].(string) // "all", "partial", "email", "pii"

	if field == "*" || field == "" {
		// Scan all fields
		data := msg.Data()
		t.scanAndMask(data, maskType)
		return msg, nil
	}

	val := evaluator.GetMsgValByPath(msg, field)
	if val == nil {
		return msg, nil
	}
	fieldVal := fmt.Sprintf("%v", val)

	var masked string
	switch maskType {
	case "email":
		masked = t.maskEmail(fieldVal)
	case "partial":
		masked = t.maskPartial(fieldVal)
	case "pii":
		masked = t.maskPII(fieldVal)
	default:
		masked = "****"
	}

	msg.SetData(field, masked)
	return msg, nil
}

func (t *MaskTransformer) scanAndMask(data map[string]interface{}, maskType string) {
	for k, v := range data {
		switch val := v.(type) {
		case string:
			var masked string
			switch maskType {
			case "email":
				masked = t.maskEmail(val)
			case "partial":
				masked = t.maskPartial(val)
			case "pii":
				masked = t.maskPII(val)
			default:
				masked = "****"
			}
			data[k] = masked
		case map[string]interface{}:
			t.scanAndMask(val, maskType)
		case []interface{}:
			for i, item := range val {
				if m, ok := item.(map[string]interface{}); ok {
					t.scanAndMask(m, maskType)
				} else if s, ok := item.(string); ok {
					var masked string
					switch maskType {
					case "email":
						masked = t.maskEmail(s)
					case "partial":
						masked = t.maskPartial(s)
					case "pii":
						masked = t.maskPII(s)
					default:
						masked = "****"
					}
					val[i] = masked
				}
			}
		}
	}
}

func (t *MaskTransformer) maskEmail(s string) string {
	parts := strings.Split(s, "@")
	if len(parts) == 2 {
		if len(parts[0]) > 1 {
			return parts[0][0:1] + "****@" + parts[1]
		}
		return "*@" + parts[1]
	}
	return "****"
}

func (t *MaskTransformer) maskPartial(s string) string {
	if len(s) > 4 {
		return s[:2] + "****" + s[len(s)-2:]
	}
	return "****"
}

func (t *MaskTransformer) maskPII(s string) string {
	return piiEngine.Mask(s)
}
