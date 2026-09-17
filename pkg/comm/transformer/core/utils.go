package core

import (
	"strings"

	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
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

// The SQL template helpers live in pkg/infra/sqlutil, next to the placeholder
// and identifier-quoting rules they depend on, so that sources and sinks can
// use them without importing a transformer package. These are aliases kept for
// the existing call sites.
type TemplateBinding = sqlutil.TemplateBinding

// ParameterizeTemplate replaces all {{ ... }} tokens in the SQL template with driver-specific placeholders
// and returns the parameterized SQL text and a corresponding args slice.
// Token content should be either a path like `source.foo` or a quoted literal. Paths are resolved against `data`.
func ParameterizeTemplate(driver, tpl string, data map[string]any) (string, []any) {
	return sqlutil.ParameterizeTemplate(driver, tpl, data)
}

// ParameterizeTemplateEx is ParameterizeTemplate with the full binding result,
// including the tokens that resolved to nothing.
func ParameterizeTemplateEx(driver, tpl string, data map[string]any) TemplateBinding {
	return sqlutil.ParameterizeTemplateEx(driver, tpl, data)
}

// AsSlice coerces v into a slice of any so it can be expanded into a SQL value
// list, reporting false for scalars -- including []byte and json.RawMessage.
func AsSlice(v any) ([]any, bool) {
	return sqlutil.AsSlice(v)
}

// GetFromMapPath resolves a dotted path in a nested map[string]any.
func GetFromMapPath(m map[string]any, path string) any {
	return sqlutil.GetFromMapPath(m, path)
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

// SplitSQLConditions splits a WHERE clause by "AND" while respecting single/double quotes.
func SplitSQLConditions(s string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	var quoteChar byte

	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c == '\'' || c == '"') && (i == 0 || s[i-1] != '\\') {
			if inQuote && c == quoteChar {
				inQuote = false
			} else if !inQuote {
				inQuote = true
				quoteChar = c
			}
		}

		// Look for " AND " (case insensitive) when not inside a quoted string
		if !inQuote && i+5 <= len(s) && strings.EqualFold(s[i:i+5], " AND ") {
			if current.Len() > 0 {
				parts = append(parts, strings.TrimSpace(current.String()))
				current.Reset()
			}
			i += 4 // Skip " AND"
			continue
		}

		current.WriteByte(c)
	}
	if current.Len() > 0 {
		p := strings.TrimSpace(current.String())
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}
