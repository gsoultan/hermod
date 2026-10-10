// Package connect builds a ready-to-use llm.Provider from a connection
// description: the adapter for its kind, plus retries, a concurrency limit
// and an optional fallback chain.
package connect

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod/pkg/llm"
	"github.com/gsoultan/hermod/pkg/llm/anthropic"
	"github.com/gsoultan/hermod/pkg/llm/gemini"
	"github.com/gsoultan/hermod/pkg/llm/openai"
)

// Spec describes one AI connection.
type Spec struct {
	// Kind is anthropic (claude), openai (chatgpt), gemini (google), one of
	// the OpenAI-compatible presets (deepseek, ollama, mistral, groq,
	// openrouter, together, xai) or openai_compatible with a BaseURL.
	Kind string
	// Name labels an openai_compatible connection; presets name themselves.
	Name    string
	BaseURL string
	APIKey  string
	// AuthHeader and JSONMode tune openai_compatible endpoints.
	AuthHeader string
	JSONMode   string
	// Model is the connection's default model, used when a request names
	// none. On a fallback it always replaces the request's model, since the
	// primary's model id means nothing to another provider.
	Model string
	// MaxConcurrency caps calls in flight on this connection (0 = no cap).
	MaxConcurrency int
	// Retry overrides llm.DefaultRetryPolicy.
	Retry     *llm.RetryPolicy
	Fallbacks []Spec
}

// Kinds lists the accepted Kind values (without aliases).
func Kinds() []string {
	return append([]string{"anthropic", "openai", "gemini", "openai_compatible"}, compatiblePresets()...)
}

func compatiblePresets() []string {
	var out []string
	for _, n := range openai.PresetNames() {
		if n != "openai" {
			out = append(out, n)
		}
	}
	return out
}

// keyless are kinds that normally run without an API key.
var keyless = map[string]bool{"ollama": true, "openai_compatible": true}

func normalise(kind string) string {
	switch k := strings.ToLower(strings.TrimSpace(kind)); k {
	case "claude":
		return "anthropic"
	case "chatgpt":
		return "openai"
	case "google":
		return "gemini"
	default:
		return k
	}
}

// Build returns the provider for s.
func Build(s Spec) (llm.Provider, error) {
	p, err := buildOne(s, false)
	if err != nil {
		return nil, err
	}
	if len(s.Fallbacks) == 0 {
		return p, nil
	}
	chain := make([]llm.Provider, 0, len(s.Fallbacks))
	for i, f := range s.Fallbacks {
		if len(f.Fallbacks) > 0 {
			return nil, fmt.Errorf("fallback %d: fallbacks cannot have their own fallbacks", i)
		}
		fp, err := buildOne(f, true)
		if err != nil {
			return nil, fmt.Errorf("fallback %d: %w", i, err)
		}
		chain = append(chain, fp)
	}
	return llm.Fallback(p, chain...), nil
}

func buildOne(s Spec, isFallback bool) (llm.Provider, error) {
	kind := normalise(s.Kind)
	if s.APIKey == "" && !keyless[kind] {
		return nil, fmt.Errorf("%s: an API key is required", kind)
	}

	var p llm.Provider
	switch kind {
	case "anthropic":
		p = anthropic.New(anthropic.Config{BaseURL: s.BaseURL, APIKey: s.APIKey})
	case "gemini":
		p = gemini.New(gemini.Config{BaseURL: s.BaseURL, APIKey: s.APIKey})
	case "openai_compatible":
		if s.BaseURL == "" {
			return nil, errors.New("openai_compatible: a base URL is required")
		}
		name := s.Name
		if name == "" {
			name = "openai_compatible"
		}
		p = openai.New(openai.Config{
			Name: name, BaseURL: s.BaseURL, APIKey: s.APIKey, AuthHeader: s.AuthHeader,
			JSONMode: openai.JSONMode(s.JSONMode), LegacyMaxTokens: true,
		})
	default:
		cfg, ok := openai.Preset(kind)
		if !ok {
			return nil, fmt.Errorf("unknown AI provider kind %q (want one of %s)", s.Kind, strings.Join(Kinds(), ", "))
		}
		if s.BaseURL != "" {
			cfg.BaseURL = s.BaseURL
		}
		cfg.APIKey = s.APIKey
		cfg.AuthHeader = s.AuthHeader
		if s.JSONMode != "" {
			cfg.JSONMode = openai.JSONMode(s.JSONMode)
		}
		p = openai.New(cfg)
	}

	policy := llm.DefaultRetryPolicy
	if s.Retry != nil {
		policy = *s.Retry
	}
	return llm.Chain(p,
		withModel(s.Model, isFallback),
		llm.WithRetry(policy),
		llm.WithConcurrencyLimit(s.MaxConcurrency),
	), nil
}

// withModel fills in the connection's model: when the request has none, or
// always when force is set.
func withModel(model string, force bool) llm.Middleware {
	return func(p llm.Provider) llm.Provider {
		if model == "" {
			return p
		}
		return modelProvider{Provider: p, model: model, force: force}
	}
}

type modelProvider struct {
	llm.Provider
	model string
	force bool
}

func (m modelProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if req.Model == "" || m.force {
		req.Model = m.model
	}
	return m.Provider.Chat(ctx, req)
}

func (m modelProvider) Embed(ctx context.Context, req llm.EmbedRequest) (llm.EmbedResponse, error) {
	e, ok := m.Provider.(llm.Embedder)
	if !ok {
		return llm.EmbedResponse{}, llm.ErrUnsupported
	}
	return e.Embed(ctx, req)
}
