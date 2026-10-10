// Package gemini is the llm adapter for Google's Gemini API
// (generativelanguage.googleapis.com, v1beta generateContent).
package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gsoultan/hermod/pkg/llm"
)

// DefaultBaseURL is the public Gemini API.
const DefaultBaseURL = "https://generativelanguage.googleapis.com"

// Config is one Gemini endpoint.
type Config struct {
	BaseURL          string
	APIKey           string
	DefaultMaxTokens int
	HTTPClient       *http.Client
}

// Provider is safe for concurrent use.
type Provider struct {
	cfg Config
}

// New returns a Provider for cfg.
func New(cfg Config) *Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.DefaultMaxTokens == 0 {
		cfg.DefaultMaxTokens = 8192
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Provider{cfg: cfg}
}

const name = "gemini"

// Name implements llm.Provider.
func (p *Provider) Name() string { return name }

type part struct {
	Text             string            `json:"text,omitempty"`
	FunctionCall     *functionCall     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
}

type functionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type functionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type generateReply struct {
	Candidates []struct {
		Content      content `json:"content"`
		FinishReason string  `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int64 `json:"promptTokenCount"`
		CandidatesTokenCount int64 `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
	ModelVersion string `json:"modelVersion"`
	ResponseID   string `json:"responseId"`
}

func (p *Provider) endpoint(model, method string) string {
	return fmt.Sprintf("%s/v1beta/models/%s:%s", p.cfg.BaseURL, url.PathEscape(model), method)
}

func (p *Provider) headers() map[string]string {
	if p.cfg.APIKey == "" {
		return nil
	}
	return map[string]string{"x-goog-api-key": p.cfg.APIKey}
}

// Chat implements llm.Provider.
func (p *Provider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if req.Model == "" {
		return llm.ChatResponse{}, llm.ErrRequiresModel(name)
	}
	if len(req.Messages) == 0 {
		return llm.ChatResponse{}, llm.ErrEmptyRequest
	}

	body := p.buildBody(req)

	start := time.Now()
	var reply generateReply
	if _, err := llm.PostJSON(ctx, p.cfg.HTTPClient, name, p.endpoint(req.Model, "generateContent"), p.headers(), body, &reply); err != nil {
		return llm.ChatResponse{}, err
	}
	out := fromReply(reply, req.Model)
	out.Latency = time.Since(start)
	return out, nil
}

func (p *Provider) buildBody(req llm.ChatRequest) map[string]any {
	contents := make([]content, 0, len(req.Messages))
	for _, m := range req.Messages {
		contents = append(contents, toContent(m))
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.cfg.DefaultMaxTokens
	}
	gen := map[string]any{"maxOutputTokens": maxTokens}
	if req.Temperature != nil {
		gen["temperature"] = *req.Temperature
	}
	if req.ResponseSchema != nil {
		gen["responseMimeType"] = "application/json"
		gen["responseJsonSchema"] = req.ResponseSchema
	}
	body := map[string]any{"contents": contents, "generationConfig": gen}
	if req.System != "" {
		body["systemInstruction"] = content{Parts: []part{{Text: req.System}}}
	}
	if len(req.Tools) > 0 {
		decls := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			d := map[string]any{"name": t.Name, "description": t.Description}
			if t.Schema != nil {
				d["parametersJsonSchema"] = t.Schema
			}
			decls = append(decls, d)
		}
		body["tools"] = []map[string]any{{"functionDeclarations": decls}}
	}

	return body
}

func fromReply(reply generateReply, model string) llm.ChatResponse {
	out := llm.ChatResponse{
		Usage:     llm.Usage{InputTokens: reply.UsageMetadata.PromptTokenCount, OutputTokens: reply.UsageMetadata.CandidatesTokenCount},
		Model:     reply.ModelVersion,
		Provider:  name,
		RequestID: reply.ResponseID,
	}
	if out.Model == "" {
		out.Model = model
	}
	if len(reply.Candidates) == 0 {
		out.StopReason = llm.StopRefusal
		return out
	}
	cand := reply.Candidates[0]
	var text strings.Builder
	for i, pt := range cand.Content.Parts {
		if pt.Text != "" {
			text.WriteString(pt.Text)
		}
		if pt.FunctionCall != nil {
			id := pt.FunctionCall.ID
			if id == "" {
				id = fmt.Sprintf("call_%d_%s", i, pt.FunctionCall.Name)
			}
			args := pt.FunctionCall.Args
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			out.ToolCalls = append(out.ToolCalls, llm.ToolCall{ID: id, Name: pt.FunctionCall.Name, Input: args})
		}
	}
	out.Text = text.String()
	out.StopReason = stopReason(cand.FinishReason)
	if len(out.ToolCalls) > 0 {
		out.StopReason = llm.StopToolUse
	}
	return out
}

func toContent(m llm.Message) content {
	if m.Role == llm.RoleAssistant {
		c := content{Role: "model"}
		if m.Text != "" {
			c.Parts = append(c.Parts, part{Text: m.Text})
		}
		for _, tc := range m.ToolCalls {
			c.Parts = append(c.Parts, part{FunctionCall: &functionCall{Name: tc.Name, Args: tc.Input}})
		}
		return c
	}
	c := content{Role: "user"}
	for _, r := range m.ToolResults {
		resp := map[string]any{"content": r.Content}
		if r.IsError {
			resp = map[string]any{"error": r.Content}
		}
		c.Parts = append(c.Parts, part{FunctionResponse: &functionResponse{Name: r.Name, Response: resp}})
	}
	if m.Text != "" {
		c.Parts = append(c.Parts, part{Text: m.Text})
	}
	return c
}

func stopReason(finish string) llm.StopReason {
	switch finish {
	case "STOP":
		return llm.StopEnd
	case "MAX_TOKENS":
		return llm.StopMaxTokens
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
		return llm.StopRefusal
	default:
		return llm.StopOther
	}
}

// Embed implements llm.Embedder using batchEmbedContents.
func (p *Provider) Embed(ctx context.Context, req llm.EmbedRequest) (llm.EmbedResponse, error) {
	if req.Model == "" {
		return llm.EmbedResponse{}, llm.ErrRequiresModel(name)
	}
	requests := make([]map[string]any, 0, len(req.Inputs))
	for _, in := range req.Inputs {
		requests = append(requests, map[string]any{
			"model":   "models/" + req.Model,
			"content": content{Parts: []part{{Text: in}}},
		})
	}
	var reply struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}
	if _, err := llm.PostJSON(ctx, p.cfg.HTTPClient, name, p.endpoint(req.Model, "batchEmbedContents"), p.headers(), map[string]any{"requests": requests}, &reply); err != nil {
		return llm.EmbedResponse{}, err
	}
	if len(reply.Embeddings) != len(req.Inputs) {
		return llm.EmbedResponse{}, fmt.Errorf("%s: got %d embeddings for %d inputs", name, len(reply.Embeddings), len(req.Inputs))
	}
	vectors := make([][]float32, len(reply.Embeddings))
	for i, e := range reply.Embeddings {
		vectors[i] = e.Values
	}
	return llm.EmbedResponse{Vectors: vectors, Model: req.Model}, nil
}
