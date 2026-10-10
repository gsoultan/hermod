package genai

import (
	"encoding/json"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
)

const redacted = "[REDACTED]"

// selectInput is the part of a message a node may send to a provider:
// inputFields (when set) is an allow-list, maskFields are replaced, and
// maskPII runs the PII engine over every string that remains. The message
// itself is never modified.
func selectInput(msg hermod.Message, config map[string]any) map[string]any {
	data := msg.Data()
	allowed := fieldList(config, "inputFields")
	out := make(map[string]any, len(data))
	if len(allowed) == 0 {
		for k, v := range data {
			out[k] = deepCopy(v)
		}
	} else {
		for _, k := range allowed {
			if v, ok := data[k]; ok {
				out[k] = deepCopy(v)
			}
		}
	}
	for _, k := range fieldList(config, "maskFields") {
		if _, ok := out[k]; ok {
			out[k] = redacted
		}
	}
	if b, _ := config["maskPII"].(bool); b {
		maskPII(out)
	}
	return out
}

// InputJSON is the part of msg a node may send to a model (see selectInput:
// inputFields, maskFields, maskPII), as JSON. Go's encoder escapes <, > and &,
// so the result cannot close a delimiter a caller wraps it in.
func InputJSON(msg hermod.Message, config map[string]any) string {
	return inputJSON(msg, config)
}

// inputJSON is selectInput rendered for a prompt.
func inputJSON(msg hermod.Message, config map[string]any) string {
	b, err := json.Marshal(selectInput(msg, config))
	if err != nil {
		return "{}"
	}
	return string(b)
}

func fieldList(config map[string]any, key string) []string {
	items := core.GetConfigStringSlice(config, key)
	if s := core.GetConfigString(config, key); s != "" {
		items = []string{s}
	}
	var out []string
	for _, f := range items {
		for part := range strings.SplitSeq(f, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func maskPII(v map[string]any) {
	for k, val := range v {
		v[k] = maskValue(val)
	}
}

func maskValue(v any) any {
	switch val := v.(type) {
	case string:
		return transformer.PIIEngine().Mask(val)
	case map[string]any:
		maskPII(val)
		return val
	case []any:
		for i := range val {
			val[i] = maskValue(val[i])
		}
		return val
	default:
		return v
	}
}

// deepCopy copies the JSON-shaped containers so masking never reaches the
// message's own maps.
func deepCopy(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, x := range val {
			out[k] = deepCopy(x)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, x := range val {
			out[i] = deepCopy(x)
		}
		return out
	default:
		return v
	}
}
