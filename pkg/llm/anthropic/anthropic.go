// Package anthropic is the llm adapter for Claude, through the official
// Anthropic Go SDK.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/gsoultan/hermod/pkg/llm"
)

const (
	// DefaultModel is used when a request names no model.
	DefaultModel = "claude-opus-5-5"
	// DefaultMaxTokens is used when a request leaves MaxTokens at zero. It
	// keeps a non-streaming response inside ordinary HTTP timeouts.
	DefaultMaxTokens = 16000
	name             = "anthropic"
)

// Config is one Anthropic endpoint.
type Config struct {
	// BaseURL overrides the API host (a proxy, or a test server).
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// Provider is safe for concurrent use.
type Provider struct {
	client sdk.Client
	hasKey bool
}

// New returns a Provider for cfg. The SDK's own retries are turned off:
// retrying is llm.WithRetry's job, so every provider retries the same way and
// a fallback can take over instead.
func New(cfg Config) *Provider {
	opts := []option.RequestOption{option.WithMaxRetries(0)}
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(strings.TrimRight(cfg.BaseURL, "/")+"/"))
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(cfg.HTTPClient))
	}
	return &Provider{client: sdk.NewClient(opts...), hasKey: cfg.APIKey != ""}
}

// Name implements llm.Provider.
func (p *Provider) Name() string { return name }

// Chat implements llm.Provider. Temperature is ignored: current Claude models
// reject sampling parameters.
func (p *Provider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if !p.hasKey {
		// The SDK would otherwise look for credentials in the process
		// environment, which belong to no particular vhost.
		return llm.ChatResponse{}, errors.New("anthropic: the connection has no API key")
	}
	if len(req.Messages) == 0 {
		return llm.ChatResponse{}, llm.ErrEmptyRequest
	}
	model := req.Model
	if model == "" {
		model = DefaultModel
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = DefaultMaxTokens
	}

	params := buildParams(req, model, maxTokens)

	start := time.Now()
	msg, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return llm.ChatResponse{}, mapError(ctx, err)
	}

	out := fromMessage(msg)
	out.Latency = time.Since(start)
	return out, nil
}

func buildParams(req llm.ChatRequest, model string, maxTokens int) sdk.MessageNewParams {
	params := sdk.MessageNewParams{
		Model:     model,
		MaxTokens: int64(maxTokens),
		Messages:  make([]sdk.MessageParam, 0, len(req.Messages)),
	}
	if req.System != "" {
		params.System = []sdk.TextBlockParam{{Text: req.System}}
	}
	for _, m := range req.Messages {
		params.Messages = append(params.Messages, toParam(m))
	}
	for _, t := range req.Tools {
		tool := sdk.ToolParam{Name: t.Name, InputSchema: inputSchema(t.Schema)}
		if t.Description != "" {
			tool.Description = sdk.String(t.Description)
		}
		params.Tools = append(params.Tools, sdk.ToolUnionParam{OfTool: &tool})
	}
	if req.ResponseSchema != nil {
		params.OutputConfig = sdk.OutputConfigParam{Format: sdk.JSONOutputFormatParam{Schema: req.ResponseSchema}}
	}

	return params
}

func fromMessage(msg *sdk.Message) llm.ChatResponse {
	out := llm.ChatResponse{
		Usage:     llm.Usage{InputTokens: msg.Usage.InputTokens, OutputTokens: msg.Usage.OutputTokens},
		Model:     msg.Model,
		Provider:  name,
		RequestID: msg.ID,
	}
	var text strings.Builder
	for _, block := range msg.Content {
		switch b := block.AsAny().(type) {
		case sdk.TextBlock:
			text.WriteString(b.Text)
		case sdk.ToolUseBlock:
			out.ToolCalls = append(out.ToolCalls, llm.ToolCall{ID: b.ID, Name: b.Name, Input: json.RawMessage(b.JSON.Input.Raw())})
		}
	}
	out.Text = text.String()
	out.StopReason = stopReason(msg.StopReason)
	return out
}

func toParam(m llm.Message) sdk.MessageParam {
	var blocks []sdk.ContentBlockParamUnion
	if m.Role == llm.RoleAssistant {
		if m.Text != "" {
			blocks = append(blocks, sdk.NewTextBlock(m.Text))
		}
		for _, tc := range m.ToolCalls {
			input := tc.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			blocks = append(blocks, sdk.NewToolUseBlock(tc.ID, input, tc.Name))
		}
		return sdk.NewAssistantMessage(blocks...)
	}
	for _, r := range m.ToolResults {
		blocks = append(blocks, sdk.NewToolResultBlock(r.CallID, r.Content, r.IsError))
	}
	if m.Text != "" {
		blocks = append(blocks, sdk.NewTextBlock(m.Text))
	}
	return sdk.NewUserMessage(blocks...)
}

// inputSchema splits a JSON Schema object into the SDK's typed fields and
// keeps everything else (additionalProperties, $defs, ...) as extra fields.
func inputSchema(schema map[string]any) sdk.ToolInputSchemaParam {
	out := sdk.ToolInputSchemaParam{}
	extra := map[string]any{}
	for k, v := range schema {
		switch k {
		case "type":
		case "properties":
			out.Properties = v
		case "required":
			if list, ok := v.([]any); ok {
				for _, item := range list {
					if s, ok := item.(string); ok {
						out.Required = append(out.Required, s)
					}
				}
			} else if list, ok := v.([]string); ok {
				out.Required = list
			}
		default:
			extra[k] = v
		}
	}
	if out.Properties == nil {
		out.Properties = map[string]any{}
	}
	if len(extra) > 0 {
		out.ExtraFields = extra
	}
	return out
}

func stopReason(r sdk.StopReason) llm.StopReason {
	switch r {
	case sdk.StopReasonEndTurn, sdk.StopReasonStopSequence:
		return llm.StopEnd
	case sdk.StopReasonToolUse:
		return llm.StopToolUse
	case sdk.StopReasonMaxTokens:
		return llm.StopMaxTokens
	case sdk.StopReasonRefusal:
		return llm.StopRefusal
	default:
		return llm.StopOther
	}
}

// maxErrorMessage bounds the provider text copied into an error.
const maxErrorMessage = 2 << 10

func mapError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	var apiErr *sdk.Error
	if errors.As(err, &apiErr) {
		out := &llm.APIError{
			Provider:  name,
			Status:    apiErr.StatusCode,
			Message:   errorText(apiErr),
			Retryable: llm.RetryableStatus(apiErr.StatusCode),
		}
		if apiErr.Response != nil {
			out.RetryAfter = llm.RetryAfter(apiErr.Response.Header)
		}
		return out
	}
	return &llm.APIError{Provider: name, Message: err.Error(), Retryable: true}
}

func errorText(e *sdk.Error) string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := e.RawJSON()
	if json.Unmarshal([]byte(msg), &body) == nil && body.Error.Message != "" {
		msg = body.Error.Message
	}
	if len(msg) > maxErrorMessage {
		msg = msg[:maxErrorMessage]
	}
	return msg
}
