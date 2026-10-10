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

type budgetBox struct{ b llm.Budget }

var budget atomic.Pointer[budgetBox]

// SetBudget gates every model call any AI node makes, the agent's included:
// the registry installs the vhost and workflow spending limits here. nil
// turns it off.
func SetBudget(b llm.Budget) {
	if b == nil {
		budget.Store(nil)
		return
	}
	budget.Store(&budgetBox{b: b})
}

// installedBudget is the llm.Budget every cached provider is built with. It
// reads SetBudget's value per call, so a provider cached before the budget was
// installed is gated all the same.
type installedBudget struct{}

func (installedBudget) Allow(ctx context.Context) error {
	if b := budget.Load(); b != nil {
		return b.b.Allow(ctx)
	}
	return nil
}

func (installedBudget) Record(ctx context.Context, rec llm.CallRecord) {
	if b := budget.Load(); b != nil {
		b.b.Record(ctx, rec)
	}
}

// vhostProvider runs every call in the scope of the vhost the engine stamped
// on the message, keeping whatever workflow the caller's context names. The
// vhost comes from hermod.VHostScoped, never from the message's data or
// metadata, so a payload cannot charge its calls to another vhost.
type vhostProvider struct {
	llm.Provider
	vhost string
}

func (v vhostProvider) scope(ctx context.Context) context.Context {
	s := llm.ScopeFrom(ctx)
	s.VHost = v.vhost
	return llm.WithScope(ctx, s)
}

func (v vhostProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	return v.Provider.Chat(v.scope(ctx), req)
}

func (v vhostProvider) Embed(ctx context.Context, req llm.EmbedRequest) (llm.EmbedResponse, error) {
	e, ok := v.Provider.(llm.Embedder)
	if !ok {
		return llm.EmbedResponse{}, llm.ErrUnsupported
	}
	return e.Embed(v.scope(ctx), req)
}

// messageVHost is the vhost the engine stamped on msg, or "".
func messageVHost(msg hermod.Message) string {
	if scoped, ok := msg.(hermod.VHostScoped); ok && scoped != nil {
		return scoped.VHost()
	}
	return ""
}

// WithWorkflow returns ctx naming the workflow whose node makes the calls in
// it, for the per-workflow spending caps.
func WithWorkflow(ctx context.Context, workflowID string) context.Context {
	s := llm.ScopeFrom(ctx)
	s.WorkflowID = workflowID
	return llm.WithScope(ctx, s)
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
//
// Every call through it is checked against, and counted in, the budget
// SetBudget installed, in the scope of msg's vhost.
func ProviderFor(config map[string]any, msg hermod.Message) (llm.Provider, string, error) {
	p, model, err := cachedProvider(config, msg)
	if err != nil {
		return nil, "", err
	}
	if vhost := messageVHost(msg); vhost != "" {
		return vhostProvider{Provider: p, vhost: vhost}, model, nil
	}
	return p, model, nil
}

func cachedProvider(config map[string]any, msg hermod.Message) (llm.Provider, string, error) {
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
	// The budget sits inside the observer, so a refused call is still
	// counted, under the budget_exceeded outcome.
	p := llm.Chain(built, llm.WithObserver(observe), llm.WithBudget(installedBudget{}))
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
	addUsage(msg, resp.Usage)
	if err != nil {
		return resp, err
	}
	if resp.StopReason == llm.StopRefusal {
		return resp, fmt.Errorf("%s declined to answer", resp.Provider)
	}
	return resp, nil
}

// Metadata keys under which a message carries the language model usage of
// every AI node it has passed through. Each node's trace step records the
// message's metadata, so a run's token totals are readable from its trace.
// Counts only: no prompt, answer or key is ever written here.
const (
	MetaAICalls        = "_hermod_ai_calls"
	MetaAIInputTokens  = "_hermod_ai_input_tokens"  //nolint:gosec // a metadata key naming a token count, not a credential
	MetaAIOutputTokens = "_hermod_ai_output_tokens" //nolint:gosec // a metadata key naming a token count, not a credential
)

// Usage is the model usage a message has accumulated.
type Usage struct {
	Calls        int64 `json:"calls"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// UsageOf reads the usage a message has accumulated.
func UsageOf(msg hermod.Message) Usage {
	get := func(key string) int64 {
		v, _ := hermod.MetadataValue(msg, key)
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	}
	return Usage{Calls: get(MetaAICalls), InputTokens: get(MetaAIInputTokens), OutputTokens: get(MetaAIOutputTokens)}
}

// addUsage adds one call to the usage msg carries. A call that failed before
// the provider answered still counts as a call.
func addUsage(msg hermod.Message, u llm.Usage) {
	if msg == nil {
		return
	}
	cur := UsageOf(msg)
	msg.SetMetadata(MetaAICalls, strconv.FormatInt(cur.Calls+1, 10))
	msg.SetMetadata(MetaAIInputTokens, strconv.FormatInt(cur.InputTokens+u.InputTokens, 10))
	msg.SetMetadata(MetaAIOutputTokens, strconv.FormatInt(cur.OutputTokens+u.OutputTokens, 10))
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
