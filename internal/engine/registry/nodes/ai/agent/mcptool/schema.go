package mcptool

import (
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

const (
	// MaxDescription caps any description or title a remote server supplies.
	// It is data shown to the model, not instructions, and a long one is
	// room for a prompt injection.
	MaxDescription = 512
	// maxSchemaBytes caps a remote input schema as JSON.
	maxSchemaBytes = 16 << 10
)

// sanitizeSchema turns a remote tool's input schema into the one shown to
// the model: an object schema of bounded size, every description and title
// cut to MaxDescription, and additionalProperties false, since arguments it
// does not declare are dropped anyway.
func sanitizeSchema(in any) (map[string]any, error) {
	if in == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}, nil
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("the remote input schema cannot be encoded: %w", err)
	}
	if len(raw) > maxSchemaBytes {
		return nil, fmt.Errorf("the remote input schema is %d bytes; the limit is %d (declare parameters on the tool instead)", len(raw), maxSchemaBytes)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil || s == nil {
		return nil, errors.New("the remote input schema is not a JSON object")
	}
	if t, ok := s["type"]; ok && t != "object" {
		return nil, fmt.Errorf("the remote input schema has type %v; a tool's input must be an object", t)
	}
	if p, ok := s["properties"]; ok {
		if _, isMap := p.(map[string]any); !isMap {
			return nil, errors.New("the remote input schema's properties is not an object")
		}
	}
	capText(s)
	s["type"] = "object"
	if _, ok := s["properties"]; !ok {
		s["properties"] = map[string]any{}
	}
	s["additionalProperties"] = false
	return s, nil
}

// capText cuts every description and title in a decoded schema.
func capText(v any) {
	switch n := v.(type) {
	case map[string]any:
		for k, child := range n {
			if s, ok := child.(string); ok && (k == "description" || k == "title") {
				n[k] = Clip(s, MaxDescription)
				continue
			}
			capText(child)
		}
	case []any:
		for _, child := range n {
			capText(child)
		}
	}
}

// BindArgs keeps the arguments a remote schema declares at its top level,
// checks that the required ones are present and that simply-typed ones have
// their type. Anything else the model sent is dropped. The remote server
// still validates the rest; this only keeps undeclared input from reaching it.
func BindArgs(schema map[string]any, in map[string]any) (map[string]any, error) {
	props, _ := schema["properties"].(map[string]any)
	out := make(map[string]any, len(props))
	for name, v := range in {
		prop, declared := props[name]
		if !declared || v == nil {
			continue
		}
		pm, _ := prop.(map[string]any)
		if typ, ok := pm["type"].(string); ok && !hasType(v, typ) {
			return nil, fmt.Errorf("argument %q must be a %s", name, typ)
		}
		out[name] = v
	}
	required, _ := schema["required"].([]any)
	for _, r := range required {
		name, _ := r.(string)
		if _, ok := out[name]; name != "" && !ok {
			return nil, fmt.Errorf("argument %q is required", name)
		}
	}
	return out, nil
}

func hasType(v any, typ string) bool {
	switch typ {
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == float64(int64(f))
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	}
	// A type this check does not know is left to the server.
	return true
}

// Clip cuts s to at most n bytes on a rune boundary, marking the cut.
func Clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…[truncated]"
}
