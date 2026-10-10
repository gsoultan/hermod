package security

import (
	"context"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"

	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("mask", &MaskTransformer{})
}

type MaskTransformer struct{}

func (t *MaskTransformer) Prepare(config map[string]any) (map[string]any, error) {
	field, _ := config["field"].(string)
	maskType, _ := config["maskType"].(string)

	config["_parsed_field"] = field
	config["_parsed_maskType"] = maskType
	return config, nil
}

func (t *MaskTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	// ABAC Check: Only mask if the user role doesn't have "pii_view" permission
	role, _ := ctx.Value("user_role").(string)
	if role == "admin" || role == "security_officer" {
		return msg, nil
	}

	var field string
	if v, ok := config["_parsed_field"].(string); ok {
		field = v
	} else {
		field, _ = config["field"].(string)
	}

	var maskType string
	if v, ok := config["_parsed_maskType"].(string); ok {
		maskType = v
	} else {
		maskType, _ = config["maskType"].(string) // "all", "partial", "email", "pii"
	}

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

	msg.SetData(field, MaskValue(fieldVal, maskType))
	return msg, nil
}

func (t *MaskTransformer) scanAndMask(data map[string]any, maskType string) {
	for k, v := range data {
		switch val := v.(type) {
		case string:
			data[k] = MaskValue(val, maskType)
		case map[string]any:
			t.scanAndMask(val, maskType)
		case []any:
			for i, item := range val {
				if m, ok := item.(map[string]any); ok {
					t.scanAndMask(m, maskType)
				} else if s, ok := item.(string); ok {
					val[i] = MaskValue(s, maskType)
				}
			}
		}
	}
}

// MaskValue masks s the way the mask node does: maskType "email" keeps the
// first letter and the domain, "partial" two characters at each end, "pii"
// masks what the PII engine finds, and anything else gives "****".
func MaskValue(s, maskType string) string {
	switch maskType {
	case "email":
		return maskEmail(s)
	case "partial":
		return maskPartial(s)
	case "pii":
		return transformer.PIIEngine().Mask(s)
	default:
		return "****"
	}
}

// MaskFields masks fields of a plain map in place, by dotted path. A path to
// an object masks every string in it; "*" masks every string in data; a
// non-string value is masked as its text. Paths data does not hold are
// skipped.
func MaskFields(data map[string]any, fields []string, maskType string) {
	t := &MaskTransformer{}
	for _, field := range fields {
		if field == "*" {
			t.scanAndMask(data, maskType)
			continue
		}
		parent, key := data, field
		for {
			head, rest, nested := strings.Cut(key, ".")
			if !nested {
				break
			}
			next, ok := parent[head].(map[string]any)
			if !ok {
				parent = nil
				break
			}
			parent, key = next, rest
		}
		if parent == nil {
			continue
		}
		switch v := parent[key].(type) {
		case nil:
		case string:
			parent[key] = MaskValue(v, maskType)
		case map[string]any:
			t.scanAndMask(v, maskType)
		default:
			parent[key] = MaskValue(fmt.Sprintf("%v", v), maskType)
		}
	}
}

func maskEmail(s string) string {
	parts := strings.Split(s, "@")
	if len(parts) == 2 {
		if len(parts[0]) > 1 {
			return parts[0][0:1] + "****@" + parts[1]
		}
		return "*@" + parts[1]
	}
	return "****"
}

func maskPartial(s string) string {
	if len(s) > 4 {
		return s[:2] + "****" + s[len(s)-2:]
	}
	return "****"
}
