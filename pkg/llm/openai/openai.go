// Package openai is the llm adapter for the OpenAI Chat Completions wire
// format. It serves OpenAI itself and every service that speaks the same
// format at another base URL: DeepSeek, Mistral, Groq, OpenRouter, Together,
// xAI, Azure OpenAI, vLLM, LM Studio and Ollama.
package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gsoultan/hermod/pkg/llm"
)

// JSONMode is how structured output is requested.
type JSONMode string

const (
	// JSONSchema uses response_format {type: json_schema}; the service
	// enforces the schema.
	JSONSchema JSONMode = "json_schema"
	// JSONObject uses response_format {type: json_object} and states the
	// schema in the system prompt, for services without json_schema support.
	JSONObject JSONMode = "json_object"
)

// Config is one endpoint.
type Config struct {
	// Name labels the provider in errors and usage, e.g. "deepseek".
	// Defaults to "openai".
	Name    string
	BaseURL string
	APIKey  string
	// AuthHeader is the header carrying the key. Empty means
	// "Authorization: Bearer <key>"; Azure uses "api-key".
	AuthHeader string
	JSONMode   JSONMode
	// LegacyMaxTokens sends max_tokens instead of max_completion_tokens,
	// which OpenAI-compatible services other than OpenAI still expect.
	LegacyMaxTokens bool
	// DefaultMaxTokens is used when a request leaves MaxTokens at zero.
	DefaultMaxTokens int
	HTTPClient       *http.Client
}

var presets = map[string]Config{
	"openai":     {Name: "openai", BaseURL: "https://api.openai.com/v1", JSONMode: JSONSchema},
	"deepseek":   {Name: "deepseek", BaseURL: "https://api.deepseek.com", JSONMode: JSONObject, LegacyMaxTokens: true},
	"ollama":     {Name: "ollama", BaseURL: "http://localhost:11434/v1", JSONMode: JSONSchema, LegacyMaxTokens: true},
	"mistral":    {Name: "mistral", BaseURL: "https://api.mistral.ai/v1", JSONMode: JSONObject, LegacyMaxTokens: true},
	"groq":       {Name: "groq", BaseURL: "https://api.groq.com/openai/v1", JSONMode: JSONObject, LegacyMaxTokens: true},
	"openrouter": {Name: "openrouter", BaseURL: "https://openrouter.ai/api/v1", JSONMode: JSONSchema, LegacyMaxTokens: true},
	"together":   {Name: "together", BaseURL: "https://api.together.xyz/v1", JSONMode: JSONObject, LegacyMaxTokens: true},
	"xai":        {Name: "xai", BaseURL: "https://api.x.ai/v1", JSONMode: JSONSchema, LegacyMaxTokens: true},
}

// Preset returns the known settings for a named service.
func Preset(name string) (Config, bool) {
	c, ok := presets[strings.ToLower(name)]
	return c, ok
}

// PresetNames lists the services Preset knows, sorted.
func PresetNames() []string {
	names := make([]string, 0, len(presets))
	for n := range presets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Provider talks to one OpenAI-compatible endpoint. It is safe for
// concurrent use.
type Provider struct {
	cfg Config
}

// New returns a Provider for cfg.
func New(cfg Config) *Provider {
	if cfg.Name == "" {
		cfg.Name = "openai"
	}
	if cfg.JSONMode == "" {
		cfg.JSONMode = JSONSchema
	}
	if cfg.DefaultMaxTokens == 0 {
		cfg.DefaultMaxTokens = 4096
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Provider{cfg: cfg}
}

// Name implements llm.Provider.
func (p *Provider) Name() string { return p.cfg.Name }

type wireFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    *string        `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatReply struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   *string        `json:"content"`
			ToolCalls []wireToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
}

func strPtr(s string) *string { return &s }

// Chat implements llm.Provider.
func (p *Provider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if req.Model == "" {
		return llm.ChatResponse{}, llm.ErrRequiresModel(p.cfg.Name)
	}
	if len(req.Messages) == 0 {
		return llm.ChatResponse{}, llm.ErrEmptyRequest
	}

	body := p.buildBody(req)

	start := time.Now()
	var reply chatReply
	if _, err := llm.PostJSON(ctx, p.cfg.HTTPClient, p.cfg.Name, p.cfg.BaseURL+"/chat/completions", p.headers(), body, &reply); err != nil {
		return llm.ChatResponse{}, err
	}
	if len(reply.Choices) == 0 {
		return llm.ChatResponse{}, &llm.APIError{Provider: p.cfg.Name, Message: "response has no choices"}
	}
	choice := reply.Choices[0]
	out := llm.ChatResponse{
		StopReason: stopReason(choice.FinishReason),
		Usage:      llm.Usage{InputTokens: reply.Usage.PromptTokens, OutputTokens: reply.Usage.CompletionTokens},
		Model:      reply.Model,
		Provider:   p.cfg.Name,
		RequestID:  reply.ID,
		Latency:    time.Since(start),
	}
	if choice.Message.Content != nil {
		out.Text = *choice.Message.Content
	}
	for _, tc := range choice.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, llm.ToolCall{ID: tc.ID, Name: tc.Function.Name, Input: json.RawMessage(tc.Function.Arguments)})
	}
	if len(out.ToolCalls) > 0 {
		out.StopReason = llm.StopToolUse
	}
	return out, nil
}

func (p *Provider) buildBody(req llm.ChatRequest) map[string]any {
	system := req.System
	body := map[string]any{"model": req.Model}
	if req.ResponseSchema != nil {
		switch p.cfg.JSONMode {
		case JSONObject:
			schema, _ := json.Marshal(req.ResponseSchema)
			system = strings.TrimSpace(system + "\n\nRespond with only a JSON object that matches this JSON Schema:\n" + string(schema))
			body["response_format"] = map[string]any{"type": "json_object"}
		default:
			body["response_format"] = map[string]any{
				"type":        "json_schema",
				"json_schema": map[string]any{"name": "response", "schema": req.ResponseSchema},
			}
		}
	}

	msgs := make([]wireMessage, 0, len(req.Messages)+1)
	if system != "" {
		msgs = append(msgs, wireMessage{Role: "system", Content: strPtr(system)})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, toWire(m)...)
	}
	body["messages"] = msgs

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.cfg.DefaultMaxTokens
	}
	if p.cfg.LegacyMaxTokens {
		body["max_tokens"] = maxTokens
	} else {
		body["max_completion_tokens"] = maxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if len(req.Tools) > 0 {
		body["tools"] = wireTools(req.Tools)
	}
	return body
}

func wireTools(specs []llm.ToolSpec) []map[string]any {
	tools := make([]map[string]any, 0, len(specs))
	for _, t := range specs {
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": schemaOrEmpty(t.Schema),
			},
		})
	}
	return tools
}

func toWire(m llm.Message) []wireMessage {
	if m.Role == llm.RoleAssistant {
		w := wireMessage{Role: "assistant"}
		if m.Text != "" {
			w.Content = strPtr(m.Text)
		}
		for _, tc := range m.ToolCalls {
			args := string(tc.Input)
			if args == "" {
				args = "{}"
			}
			w.ToolCalls = append(w.ToolCalls, wireToolCall{ID: tc.ID, Type: "function", Function: wireFunction{Name: tc.Name, Arguments: args}})
		}
		return []wireMessage{w}
	}
	var out []wireMessage
	for _, r := range m.ToolResults {
		content := r.Content
		if r.IsError {
			content = "ERROR: " + content
		}
		out = append(out, wireMessage{Role: "tool", Content: strPtr(content), ToolCallID: r.CallID})
	}
	if m.Text != "" {
		out = append(out, wireMessage{Role: "user", Content: strPtr(m.Text)})
	}
	return out
}

func schemaOrEmpty(s map[string]any) map[string]any {
	if s == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return s
}

func stopReason(finish string) llm.StopReason {
	switch finish {
	case "stop":
		return llm.StopEnd
	case "tool_calls", "function_call":
		return llm.StopToolUse
	case "length":
		return llm.StopMaxTokens
	case "content_filter":
		return llm.StopRefusal
	default:
		return llm.StopOther
	}
}

func (p *Provider) headers() map[string]string {
	if p.cfg.APIKey == "" {
		return nil
	}
	if p.cfg.AuthHeader != "" && !strings.EqualFold(p.cfg.AuthHeader, "Authorization") {
		return map[string]string{p.cfg.AuthHeader: p.cfg.APIKey}
	}
	return map[string]string{"Authorization": "Bearer " + p.cfg.APIKey}
}

// Embed implements llm.Embedder.
func (p *Provider) Embed(ctx context.Context, req llm.EmbedRequest) (llm.EmbedResponse, error) {
	if req.Model == "" {
		return llm.EmbedResponse{}, llm.ErrRequiresModel(p.cfg.Name)
	}
	var reply struct {
		Model string `json:"model"`
		Data  []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens int64 `json:"prompt_tokens"`
		} `json:"usage"`
	}
	body := map[string]any{"model": req.Model, "input": req.Inputs}
	if _, err := llm.PostJSON(ctx, p.cfg.HTTPClient, p.cfg.Name, p.cfg.BaseURL+"/embeddings", p.headers(), body, &reply); err != nil {
		return llm.EmbedResponse{}, err
	}
	vectors := make([][]float32, len(req.Inputs))
	for _, d := range reply.Data {
		if d.Index < 0 || d.Index >= len(vectors) {
			return llm.EmbedResponse{}, fmt.Errorf("%s: embedding index %d out of range", p.cfg.Name, d.Index)
		}
		vectors[d.Index] = d.Embedding
	}
	return llm.EmbedResponse{Vectors: vectors, Usage: llm.Usage{InputTokens: reply.Usage.PromptTokens}, Model: reply.Model}, nil
}
