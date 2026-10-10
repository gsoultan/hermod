// Package aimetrics turns the record of every language model call an AI node
// makes into the Prometheus metrics in pkg/engine/telemetry.
//
// It is an llm.Observer: cmd/hermod hands Observe to genai.SetObserver at
// startup. A CallRecord carries the provider, model, usage, latency and
// outcome of a call and nothing of its content, so neither a prompt nor a key
// can reach a label from here.
package aimetrics

import (
	"context"
	"errors"
	"strings"

	"github.com/gsoultan/hermod/pkg/engine/telemetry"
	"github.com/gsoultan/hermod/pkg/llm"
)

// maxLabelLen bounds a provider or model label. Model ids are configuration,
// not data, but a label is forever: one pasted wrongly must not become a
// series named after a paragraph.
const maxLabelLen = 64

// Outcome classes. A fixed set, so the outcome label has bounded cardinality.
const (
	OutcomeOK            = "ok"
	OutcomeRefused       = "refused"
	OutcomeTimeout       = "timeout"
	OutcomeCanceled      = "canceled"
	OutcomeBudget        = "budget_exceeded"
	OutcomeRateLimited   = "rate_limited"
	OutcomeProviderError = "provider_error"
	OutcomeRejected      = "rejected"
	OutcomeError         = "error"
)

// Outcome classifies a call: what an operator would alert on differently.
func Outcome(rec llm.CallRecord) string {
	if rec.Err == nil {
		if rec.StopReason == llm.StopRefusal {
			return OutcomeRefused
		}
		return OutcomeOK
	}
	var apiErr *llm.APIError
	switch {
	case errors.Is(rec.Err, context.DeadlineExceeded):
		return OutcomeTimeout
	case errors.Is(rec.Err, context.Canceled):
		return OutcomeCanceled
	case errors.Is(rec.Err, llm.ErrBudgetExceeded):
		return OutcomeBudget
	case errors.As(rec.Err, &apiErr):
		switch {
		case apiErr.Status == 429:
			return OutcomeRateLimited
		case apiErr.Retryable:
			return OutcomeProviderError
		default:
			return OutcomeRejected
		}
	default:
		return OutcomeError
	}
}

// Label bounds a provider or model name for use as a label value: at most
// maxLabelLen bytes, and only characters a model id is made of.
func Label(s string) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= maxLabelLen {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == ':', r == '/', r == '@':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

// Observe records one call. It only touches counters, so it never blocks the
// call it observes.
func Observe(_ context.Context, rec llm.CallRecord) {
	provider, model := Label(rec.Provider), Label(rec.Model)
	telemetry.AICalls.WithLabelValues(provider, model, Outcome(rec)).Inc()
	if rec.Usage.InputTokens > 0 {
		telemetry.AITokens.WithLabelValues(provider, model, "input").Add(float64(rec.Usage.InputTokens))
	}
	if rec.Usage.OutputTokens > 0 {
		telemetry.AITokens.WithLabelValues(provider, model, "output").Add(float64(rec.Usage.OutputTokens))
	}
	telemetry.AICallDuration.WithLabelValues(provider, model).Observe(rec.Latency.Seconds())
}
