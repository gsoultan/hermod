package core

import (
	"strings"

	"github.com/user/hermod/pkg/infra/sqlutil"
)

func GetConfigString(config map[string]any, key string) string {
	if v, ok := config[key].(string); ok {
		return v
	}
	return ""
}

func GetConfigStringSlice(config map[string]any, key string) []string {
	if v, ok := config[key].([]any); ok {
		res := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				res = append(res, s)
			}
		}
		return res
	}
	if v, ok := config[key].([]string); ok {
		return v
	}
	return nil
}

// ParameterizeTemplate replaces all {{ ... }} tokens in the SQL template with driver-specific placeholders
// and returns the parameterized SQL text and a corresponding args slice.
// Token content should be either a path like `source.foo` or a quoted literal. Paths are resolved against `data`.
func ParameterizeTemplate(driver, tpl string, data map[string]any) (string, []any) {
	var out strings.Builder
	var args []any
	i := 0
	nextIdx := 1
	for i < len(tpl) {
		if i+1 < len(tpl) && tpl[i] == '{' && tpl[i+1] == '{' {
			// find closing }}
			j := i + 2
			for j+1 < len(tpl) {
				if tpl[j] == '}' && tpl[j+1] == '}' {
					break
				}
				j++
			}
			if j+1 >= len(tpl) {
				// no closing, write rest and break
				out.WriteString(tpl[i:])
				break
			}
			token := strings.TrimSpace(tpl[i+2 : j])
			// Resolve token value
			var val any
			switch {
			case strings.HasPrefix(token, "'") && strings.HasSuffix(token, "'"):
				val = strings.Trim(token, "'")
			case strings.HasPrefix(token, "\"") && strings.HasSuffix(token, "\""):
				val = strings.Trim(token, "\"")
			default:
				// allow optional source. prefix or leading dot
				token = strings.TrimPrefix(token, "source.")
				token = strings.TrimPrefix(token, ".")
				// Use evaluator to get message value by path semantics
				// We only have a map here, so mimic evaluator.GetMsgValByPath on the map
				val = GetFromMapPath(data, token)
			}
			out.WriteString(sqlutil.Placeholder(driver, nextIdx))
			nextIdx++
			args = append(args, val)
			i = j + 2
		} else {
			out.WriteByte(tpl[i])
			i++
		}
	}
	return out.String(), args
}

// GetFromMapPath resolves a dotted path in a nested map[string]any.
func GetFromMapPath(m map[string]any, path string) any {
	if m == nil || path == "" {
		return nil
	}
	parts := strings.Split(path, ".")
	var cur any = m
	for _, p := range parts {
		if mm, ok := cur.(map[string]any); ok {
			cur = mm[p]
		} else {
			return nil
		}
	}
	return cur
}

func SplitComma(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	res := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			res = append(res, p)
		}
	}
	return res
}
