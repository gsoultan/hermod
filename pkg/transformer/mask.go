package transformer

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
)

var piiRegexes = []*regexp.Regexp{
	regexp.MustCompile(`\b(?:4[0-9]{12}(?:[0-9]{3})?|5[1-5][0-9]{14}|6(?:011|5[0-9][0-9])[0-9]{12}|3[47][0-9]{13}|3(?:0[0-5]|[68][0-9])[0-9]{11}|(?:2131|1800|35\d{3})\d{11})\b`), // Credit Card
	regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),                                               // SSN
	regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`),                                         // IPv4
	regexp.MustCompile(`\b(?:[0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}\b`),                        // IPv6
	regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Z|a-z]{2,}\b`),                 // Email
	regexp.MustCompile(`\b(?:\+?1[-. ]?)?\(?([0-9]{3})\)?[-. ]?([0-9]{3})[-. ]?([0-9]{4})\b`), // Phone (US)
	regexp.MustCompile(`\b[A-Z]{2}[0-9]{2}[A-Z0-9]{11,30}\b`),                                 // IBAN
}

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
	res := s
	for _, re := range piiRegexes {
		res = re.ReplaceAllString(res, "****")
	}
	return res
}
