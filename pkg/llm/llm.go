// Package llm is Hermod's one interface to large language model providers.
//
// Every AI feature (the AI nodes, the agent loop, error analysis) talks to a
// Provider and never to a vendor API. Adapters live in sub-packages
// (anthropic, openai, gemini) and the decorators in this package add the
// behaviour every call needs whatever the vendor: retries, concurrency
// limits, fallback and usage accounting.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Role is who wrote a message in a conversation.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn. A user turn carries Text and/or ToolResults; an
// assistant turn carries Text and/or ToolCalls.
type Message struct {
	Role        Role
	Text        string
	ToolCalls   []ToolCall
	ToolResults []ToolResult
}

// ToolSpec describes a tool the model may call. Schema is a JSON Schema
// object for the tool's input.
type ToolSpec struct {
	Name        string
	Description string
	Schema      map[string]any
}

// ToolCall is the model asking for a tool to be run.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolResult answers a ToolCall. Name is repeated because some providers
// (Gemini) match results to calls by name rather than by id.
type ToolResult struct {
	CallID  string
	Name    string
	Content string
	IsError bool
}

// ChatRequest is a provider-neutral chat completion request.
type ChatRequest struct {
	// Model is the provider's model id. Adapters that have a sensible
	// default use it when this is empty; the others reject the request.
	Model    string
	System   string
	Messages []Message
	Tools    []ToolSpec
	// ResponseSchema, when set, asks for a JSON object matching this JSON
	// Schema, using the provider's native structured output where it has one.
	ResponseSchema map[string]any
	// MaxTokens caps the response. Zero means the adapter's default.
	MaxTokens int
	// Temperature is sent only when set, and only to providers whose current
	// models accept it.
	Temperature *float64
}

// StopReason says why the model stopped.
type StopReason string

const (
	StopEnd       StopReason = "end"
	StopToolUse   StopReason = "tool_use"
	StopMaxTokens StopReason = "max_tokens"
	StopRefusal   StopReason = "refusal"
	StopOther     StopReason = "other"
)

// Usage is what a call consumed.
type Usage struct {
	InputTokens  int64
	OutputTokens int64
}

// Add returns the sum of two usages.
func (u Usage) Add(o Usage) Usage {
	return Usage{InputTokens: u.InputTokens + o.InputTokens, OutputTokens: u.OutputTokens + o.OutputTokens}
}

// ChatResponse is a provider-neutral chat completion response.
type ChatResponse struct {
	Text       string
	ToolCalls  []ToolCall
	StopReason StopReason
	Usage      Usage
	// Model is the model that actually answered (it can differ from the one
	// asked for when a fallback served the call).
	Model     string
	Provider  string
	RequestID string
	Latency   time.Duration
}

// EmbedRequest asks for one vector per input.
type EmbedRequest struct {
	Model  string
	Inputs []string
}

// EmbedResponse holds vectors in the order of EmbedRequest.Inputs.
type EmbedResponse struct {
	Vectors [][]float32
	Usage   Usage
	Model   string
}

// Provider is a chat model endpoint.
type Provider interface {
	// Name is the provider kind, e.g. "anthropic", "openai", "gemini".
	Name() string
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// Embedder is a provider that can also produce embeddings.
type Embedder interface {
	Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error)
}

// ErrUnsupported is returned for a capability a provider does not have.
var ErrUnsupported = errors.New("llm: not supported by this provider")

// APIError is a failed provider call. Retryable says whether the same request
// may succeed later (rate limits, overload, server errors, network errors).
type APIError struct {
	Provider   string
	Status     int
	Message    string
	Retryable  bool
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("%s: %s", e.Provider, e.Message)
	}
	return fmt.Sprintf("%s: status %d: %s", e.Provider, e.Status, e.Message)
}

// IsRetryable reports whether err is an APIError marked retryable.
func IsRetryable(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Retryable
}

// RetryableStatus is the HTTP status rule every adapter shares.
func RetryableStatus(status int) bool {
	return status == 408 || status == 409 || status == 429 || status >= 500
}
