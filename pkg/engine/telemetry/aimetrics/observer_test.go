package aimetrics

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/gsoultan/hermod/pkg/engine/telemetry"
	"github.com/gsoultan/hermod/pkg/llm"
)

func TestOutcome(t *testing.T) {
	cases := []struct {
		name string
		rec  llm.CallRecord
		want string
	}{
		{"ok", llm.CallRecord{StopReason: llm.StopEnd}, OutcomeOK},
		{"refusal", llm.CallRecord{StopReason: llm.StopRefusal}, OutcomeRefused},
		{"timeout", llm.CallRecord{Err: fmt.Errorf("wrapped: %w", context.DeadlineExceeded)}, OutcomeTimeout},
		{"canceled", llm.CallRecord{Err: context.Canceled}, OutcomeCanceled},
		{"budget", llm.CallRecord{Err: fmt.Errorf("x: %w", llm.ErrBudgetExceeded)}, OutcomeBudget},
		{"rate limited", llm.CallRecord{Err: &llm.APIError{Provider: "openai", Status: 429, Retryable: true}}, OutcomeRateLimited},
		{"provider 5xx", llm.CallRecord{Err: &llm.APIError{Provider: "openai", Status: 503, Retryable: true}}, OutcomeProviderError},
		{"bad request", llm.CallRecord{Err: &llm.APIError{Provider: "openai", Status: 400}}, OutcomeRejected},
		{"other", llm.CallRecord{Err: errors.New("boom")}, OutcomeError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Outcome(tc.rec); got != tc.want {
				t.Fatalf("Outcome = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestObserveRecordsCallsTokensAndLatency(t *testing.T) {
	model := "obs-test-model-" + t.Name()
	calls := telemetry.AICalls.WithLabelValues("anthropic", model, OutcomeOK)
	in := telemetry.AITokens.WithLabelValues("anthropic", model, "input")
	out := telemetry.AITokens.WithLabelValues("anthropic", model, "output")
	beforeCalls, beforeIn, beforeOut := testutil.ToFloat64(calls), testutil.ToFloat64(in), testutil.ToFloat64(out)
	beforeHist := testutil.CollectAndCount(telemetry.AICallDuration)

	Observe(t.Context(), llm.CallRecord{
		Provider: "anthropic", Model: model, StopReason: llm.StopEnd,
		Usage: llm.Usage{InputTokens: 120, OutputTokens: 30}, Latency: 1500 * time.Millisecond,
	})

	if d := testutil.ToFloat64(calls) - beforeCalls; d != 1 {
		t.Errorf("calls delta = %v, want 1", d)
	}
	if d := testutil.ToFloat64(in) - beforeIn; d != 120 {
		t.Errorf("input tokens delta = %v, want 120", d)
	}
	if d := testutil.ToFloat64(out) - beforeOut; d != 30 {
		t.Errorf("output tokens delta = %v, want 30", d)
	}
	if got := testutil.CollectAndCount(telemetry.AICallDuration); got != beforeHist+1 {
		t.Errorf("latency series = %d, want %d", got, beforeHist+1)
	}
}

// A model name is configuration, so a label built from it is bounded and
// can never carry an arbitrary string such as a prompt pasted in by mistake.
func TestObserveBoundsLabels(t *testing.T) {
	long := strings.Repeat("m", 500) + "\nsecret prompt"
	Observe(t.Context(), llm.CallRecord{Provider: "", Model: long, StopReason: llm.StopEnd})

	got := testutil.ToFloat64(telemetry.AICalls.WithLabelValues("unknown", Label(long), OutcomeOK))
	if got < 1 {
		t.Fatalf("call was not recorded under the bounded label")
	}
	if l := Label(long); len(l) > maxLabelLen || strings.ContainsAny(l, "\n ") {
		t.Fatalf("label %q is not bounded/sanitised", l)
	}
}
