// Package genai holds the workflow nodes that call a large language model:
// ai_prompt, ai_extract and ai_embed. Every call goes through pkg/llm, so any
// provider it supports (Claude, OpenAI, Gemini, DeepSeek, Ollama, ...) works
// in every node.
package genai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/llm"
	"github.com/gsoultan/hermod/pkg/llm/connect"
)

// DefaultTimeout bounds one node's call to the model, retries included.
const DefaultTimeout = 60 * time.Second

type observerBox struct{ obs llm.Observer }

var observer atomic.Pointer[observerBox]

// SetObserver receives a record of every model call any AI node makes; the
// registry uses it for metrics and the audit log. nil turns it off.
func SetObserver(obs llm.Observer) {
	if obs == nil {
		observer.Store(nil)
		return
	}
	observer.Store(&observerBox{obs: obs})
}

func observe(ctx context.Context, rec llm.CallRecord) {
	if b := observer.Load(); b != nil {
		b.obs(ctx, rec)
	}
}

// maxCachedProviders bounds the provider cache. A provider is cheap to
// rebuild; the cache exists so that a connection's concurrency limit is
// shared by every message rather than per message.
const maxCachedProviders = 256

var (
	providersMu sync.Mutex
	providers   = map[string]llm.Provider{}
)

// connection reads a node's connection settings. The base URL and the key
// decide where a credential is sent, so they are resolved for the message's
// vhost only ({{secret("NAME")}} works; row fields render empty).
func connection(config map[string]any, msg hermod.Message, prefix string) connect.Spec {
	get := func(key string) string {
		if prefix != "" {
			key = prefix + strings.ToUpper(key[:1]) + key[1:]
		}
		return strings.TrimSpace(core.GetConfigString(config, key))
	}
	scoped := func(key string) string {
		v := get(key)
		if msg != nil {
			return strings.TrimSpace(evaluator.ResolveTemplateScoped(v, msg))
		}
		return strings.TrimSpace(evaluator.ResolveTemplate(v, nil))
	}
	s := connect.Spec{
		Kind:       get("provider"),
		BaseURL:    scoped("baseUrl"),
		APIKey:     scoped("apiKey"),
		AuthHeader: get("authHeader"),
		JSONMode:   get("jsonMode"),
		Model:      get("model"),
	}
	if n, err := strconv.Atoi(get("maxConcurrency")); err == nil {
		s.MaxConcurrency = n
	}
	return s
}

// ProviderFor returns the provider a node's config names, and the model to
// ask for. A fallback connection is configured with the fallback* keys
// (fallbackProvider, fallbackModel, fallbackApiKey, fallbackBaseUrl).
func ProviderFor(config map[string]any, msg hermod.Message) (llm.Provider, string, error) {
	spec := connection(config, msg, "")
	if spec.Kind == "" {
		return nil, "", errors.New("no AI provider configured")
	}
	if fb := connection(config, msg, "fallback"); fb.Kind != "" {
		spec.Fallbacks = []connect.Spec{fb}
	}

	key := cacheKey(spec)
	providersMu.Lock()
	defer providersMu.Unlock()
	if p, ok := providers[key]; ok {
		return p, spec.Model, nil
	}
	built, err := connect.Build(spec)
	if err != nil {
		return nil, "", err
	}
	p := llm.Chain(built, llm.WithObserver(observe))
	if len(providers) >= maxCachedProviders {
		clear(providers)
	}
	providers[key] = p
	return p, spec.Model, nil
}

// cacheKey digests a connection. The key only ever appears hashed.
func cacheKey(s connect.Spec) string {
	h := sha256.New()
	write := func(spec connect.Spec) {
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00",
			spec.Kind, spec.BaseURL, spec.APIKey, spec.AuthHeader, spec.JSONMode, spec.Model, spec.MaxConcurrency)
	}
	write(s)
	for _, f := range s.Fallbacks {
		write(f)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// callOptions are the per-call settings every AI node shares.
func callOptions(config map[string]any) (maxTokens int, temperature *float64, timeout time.Duration) {
	maxTokens, _ = strconv.Atoi(core.GetConfigString(config, "maxTokens"))
	if t, err := strconv.ParseFloat(core.GetConfigString(config, "temperature"), 64); err == nil {
		temperature = &t
	}
	timeout = DefaultTimeout
	if d, err := time.ParseDuration(core.GetConfigString(config, "timeout")); err == nil && d > 0 {
		timeout = d
	}
	return maxTokens, temperature, timeout
}

// chat runs one request for a node, bounded by the node's timeout.
func chat(ctx context.Context, config map[string]any, msg hermod.Message, req llm.ChatRequest) (llm.ChatResponse, error) {
	p, model, err := ProviderFor(config, msg)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	maxTokens, temperature, timeout := callOptions(config)
	if req.Model == "" {
		req.Model = model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = maxTokens
	}
	if req.Temperature == nil {
		req.Temperature = temperature
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := p.Chat(ctx, req)
	if err != nil {
		return resp, err
	}
	if resp.StopReason == llm.StopRefusal {
		return resp, fmt.Errorf("%s declined to answer", resp.Provider)
	}
	return resp, nil
}

// writeUsage records a call's usage in the field the node names, if any.
func writeUsage(msg hermod.Message, config map[string]any, resp llm.ChatResponse) {
	field := core.GetConfigString(config, "usageField")
	if field == "" {
		return
	}
	msg.SetData(field, map[string]any{
		"provider":      resp.Provider,
		"model":         resp.Model,
		"input_tokens":  resp.Usage.InputTokens,
		"output_tokens": resp.Usage.OutputTokens,
		"latency_ms":    resp.Latency.Milliseconds(),
	})
}
