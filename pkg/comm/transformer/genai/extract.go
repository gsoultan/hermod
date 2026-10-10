package genai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/xeipuuv/gojsonschema"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/llm"
)

func init() {
	transformer.Register("ai_extract", &ExtractTransformer{})
}

// ExtractTransformer pulls structured fields out of a message: the model is
// asked for a JSON object matching a JSON Schema, the answer is validated,
// and one retry is made with the validation errors before the node fails.
//
// Config: schema (required, JSON Schema as text or object), instructions,
// inputFields, maskFields, maskPII, targetField ("" merges the fields into the
// message), usageField, and the connection keys read by ProviderFor.
type ExtractTransformer struct{}

const extractSystem = "You extract structured data. Use only information present in the input. " +
	"Use null for anything the input does not state. Answer with a single JSON object that matches the schema."

// Transform implements transformer.Transformer.
func (t *ExtractTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	schema, validator, err := loadSchema(config["schema"])
	if err != nil {
		return nil, fmt.Errorf("ai_extract: %w", err)
	}
	user := "Input:\n" + inputJSON(msg, config)
	if instr := strings.TrimSpace(core.GetConfigString(config, "instructions")); instr != "" {
		user = instr + "\n\n" + user
	}
	req := llm.ChatRequest{
		System:         extractSystem,
		Messages:       []llm.Message{{Role: llm.RoleUser, Text: user}},
		ResponseSchema: schema,
	}

	var usage llm.Usage
	for attempt := range 2 {
		resp, err := chat(ctx, config, msg, req)
		if err != nil {
			return nil, fmt.Errorf("ai_extract: %w", err)
		}
		usage = usage.Add(resp.Usage)
		obj, problems := validate(validator, resp.Text)
		if problems == "" {
			resp.Usage = usage
			writeObject(msg, core.GetConfigString(config, "targetField"), obj)
			writeUsage(msg, config, resp)
			return msg, nil
		}
		if attempt == 1 {
			return nil, fmt.Errorf("ai_extract: the answer did not match the schema: %s", problems)
		}
		req.Messages = append(req.Messages,
			llm.Message{Role: llm.RoleAssistant, Text: resp.Text},
			llm.Message{Role: llm.RoleUser, Text: "That answer does not match the schema: " + problems + "\nAnswer again with only the corrected JSON object."},
		)
	}
	return nil, errors.New("ai_extract: unreachable")
}

// loadSchema accepts the schema as JSON text or as an already-decoded object.
func loadSchema(raw any) (map[string]any, *gojsonschema.Schema, error) {
	var schema map[string]any
	switch v := raw.(type) {
	case map[string]any:
		schema = v
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, nil, errors.New("a JSON Schema is required")
		}
		if err := json.Unmarshal([]byte(v), &schema); err != nil {
			return nil, nil, fmt.Errorf("the schema is not valid JSON: %w", err)
		}
	default:
		return nil, nil, errors.New("a JSON Schema is required")
	}
	compiled, err := gojsonschema.NewSchema(gojsonschema.NewGoLoader(schema))
	if err != nil {
		return nil, nil, fmt.Errorf("the schema is not a valid JSON Schema: %w", err)
	}
	return schema, compiled, nil
}

// validate parses text as a JSON object and checks it; problems is "" when
// the object is valid.
func validate(s *gojsonschema.Schema, text string) (map[string]any, string) {
	obj, err := parseJSONObject(text)
	if err != nil {
		return nil, err.Error()
	}
	res, err := s.Validate(gojsonschema.NewGoLoader(obj))
	if err != nil {
		return nil, err.Error()
	}
	if res.Valid() {
		return obj, ""
	}
	msgs := make([]string, 0, len(res.Errors()))
	for _, e := range res.Errors() {
		msgs = append(msgs, e.String())
	}
	return nil, strings.Join(msgs, "; ")
}
