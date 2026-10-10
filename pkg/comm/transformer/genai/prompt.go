package genai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/comm/transformer/genai/memory"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/llm"
)

func init() {
	transformer.Register("ai_prompt", &PromptTransformer{})
}

// DefaultPromptField is where ai_prompt writes a text answer by default.
const DefaultPromptField = "ai_output"

// PromptTransformer sends a templated prompt to a model and writes the answer
// into the message.
//
// Config: prompt (required; {{.field}} tokens read the message), system,
// includeData (append the selected input as JSON), inputFields, maskFields,
// maskPII, outputMode ("text" or "json"), targetField, usageField, memory
// (see package memory: earlier exchanges of the message's conversation are
// sent ahead of it, and the new exchange is remembered), and the connection
// keys read by ProviderFor.
type PromptTransformer struct{}

// Transform implements transformer.Transformer.
func (t *PromptTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	tmpl := core.GetConfigString(config, "prompt")
	if strings.TrimSpace(tmpl) == "" {
		return nil, errors.New("ai_prompt: a prompt is required")
	}
	user := evaluator.ResolveTemplateMsg(tmpl, msg)
	if include, _ := config["includeData"].(bool); include {
		user += "\n\nInput:\n" + inputJSON(msg, config)
	}
	jsonOut := core.GetConfigString(config, "outputMode") == "json"
	system := core.GetConfigString(config, "system")
	if jsonOut {
		system = strings.TrimSpace(system + "\n\nAnswer with a single JSON object and nothing else.")
	}

	conv, history, err := recall(ctx, msg, config)
	if err != nil {
		return nil, fmt.Errorf("ai_prompt: %w", err)
	}
	resp, err := chat(ctx, config, msg, llm.ChatRequest{
		System:   system,
		Messages: append(history, llm.Message{Role: llm.RoleUser, Text: user}),
	})
	if err != nil {
		return nil, fmt.Errorf("ai_prompt: %w", err)
	}
	if conv != nil {
		if err := conv.Append(ctx, user, strings.TrimSpace(resp.Text)); err != nil {
			return nil, fmt.Errorf("ai_prompt: %w", err)
		}
	}
	target := core.GetConfigString(config, "targetField")
	if !jsonOut {
		if target == "" {
			target = DefaultPromptField
		}
		msg.SetData(target, strings.TrimSpace(resp.Text))
	} else {
		obj, err := parseJSONObject(resp.Text)
		if err != nil {
			return nil, fmt.Errorf("ai_prompt: %w", err)
		}
		writeObject(msg, target, obj)
	}
	writeUsage(msg, config, resp)
	return msg, nil
}

// recall loads the node's conversation memory, when it has one: the
// conversation to extend once the model answers, and its earlier turns.
func recall(ctx context.Context, msg hermod.Message, config map[string]any) (*memory.Conversation, []llm.Message, error) {
	cfg, on, err := memory.Parse(config[memory.ConfigKey])
	if err != nil || !on {
		return nil, nil, err
	}
	conv, err := memory.Open(ctx, msg, cfg)
	if err != nil {
		return nil, nil, err
	}
	turns, err := conv.History(ctx)
	if err != nil {
		return nil, nil, err
	}
	history := make([]llm.Message, 0, len(turns)+1)
	for _, t := range turns {
		history = append(history, llm.Message{Role: llm.Role(t.Role), Text: t.Text})
	}
	return conv, history, nil
}

// parseJSONObject reads a model's JSON answer, tolerating a Markdown fence
// or text around the object.
func parseJSONObject(text string) (map[string]any, error) {
	s := strings.TrimSpace(text)
	var obj map[string]any
	if json.Unmarshal([]byte(s), &obj) == nil {
		return obj, nil
	}
	start, end := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(s[start:end+1]), &obj); err == nil {
			return obj, nil
		}
	}
	excerpt := s
	if len(excerpt) > 200 {
		excerpt = excerpt[:200] + "..."
	}
	return nil, fmt.Errorf("the model did not answer with a JSON object: %q", excerpt)
}

// writeObject nests obj under target, or merges its keys when target is "".
func writeObject(msg hermod.Message, target string, obj map[string]any) {
	if target != "" {
		msg.SetData(target, obj)
		return
	}
	for k, v := range obj {
		msg.SetData(k, v)
	}
}
