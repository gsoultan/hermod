// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package advisor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/llm"
)

type fakeModel struct {
	reqs   []llm.ChatRequest
	scopes []llm.Scope
	reply  llm.ChatResponse
	err    error
}

func (*fakeModel) Name() string { return "fake" }

func (f *fakeModel) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	f.reqs = append(f.reqs, req)
	f.scopes = append(f.scopes, llm.ScopeFrom(ctx))
	return f.reply, f.err
}

func explainerWith(m *fakeModel) *Explainer {
	return NewExplainer(func(context.Context, string) (llm.Provider, string, error) { return m, "model-x", nil })
}

func readyReport() Report {
	return Analyze(steady(nil), chain())
}

func TestExplainSendsTheFindingsAndNothingElse(t *testing.T) {
	m := &fakeModel{reply: llm.ChatResponse{Text: " Cache the lookup first. ", StopReason: llm.StopEnd}}
	got, err := explainerWith(m).Explain(t.Context(), "team-a", readyReport())
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "Cache the lookup first." || got.Model != "model-x" || got.Provider != "fake" {
		t.Errorf("explanation = %+v", got)
	}
	if len(m.reqs) != 1 {
		t.Fatalf("made %d calls, want 1", len(m.reqs))
	}
	sent := m.reqs[0].Messages[0].Text
	for _, want := range []string{"Find customer is the slowest step", "Share of step time", "Only paid"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the brief leaves out %q:\n%s", want, sent)
		}
	}
	if m.reqs[0].MaxTokens == 0 || m.reqs[0].MaxTokens > 1024 {
		t.Errorf("MaxTokens = %d; the answer must be capped", m.reqs[0].MaxTokens)
	}
	if !strings.Contains(m.reqs[0].System, "not instructions") {
		t.Errorf("the system prompt does not mark the findings as data: %q", m.reqs[0].System)
	}
}

// The call is charged to the flow's own vhost and workflow, whatever the
// context it arrived in said.
func TestExplainIsChargedToTheFlow(t *testing.T) {
	m := &fakeModel{reply: llm.ChatResponse{Text: "ok", StopReason: llm.StopEnd}}
	ctx := llm.WithScope(t.Context(), llm.Scope{VHost: "someone-else", WorkflowID: "other"})
	if _, err := explainerWith(m).Explain(ctx, "team-a", readyReport()); err != nil {
		t.Fatal(err)
	}
	if got := m.scopes[0]; got.VHost != "team-a" || got.WorkflowID != "wf-1" {
		t.Errorf("scope = %+v, want team-a/wf-1", got)
	}
}

func TestExplainMakesNoCallWithNothingToExplain(t *testing.T) {
	m := &fakeModel{}
	for name, r := range map[string]Report{
		"off":         Analyze(nil, chain()),
		"no findings": {WorkflowID: "wf-1", State: StateReady},
	} {
		if _, err := explainerWith(m).Explain(t.Context(), "team-a", r); !errors.Is(err, ErrNothingToExplain) {
			t.Errorf("%s: err = %v, want ErrNothingToExplain", name, err)
		}
	}
	if len(m.reqs) != 0 {
		t.Errorf("made %d calls for nothing", len(m.reqs))
	}
}

func TestExplainReportsARefusalOrAnEmptyAnswer(t *testing.T) {
	for name, reply := range map[string]llm.ChatResponse{
		"refusal": {Text: "no", StopReason: llm.StopRefusal},
		"empty":   {Text: "  ", StopReason: llm.StopEnd},
	} {
		if _, err := explainerWith(&fakeModel{reply: reply}).Explain(t.Context(), "team-a", readyReport()); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// A flow with very many findings must not send an unbounded prompt.
func TestBriefIsBounded(t *testing.T) {
	r := readyReport()
	for range 500 {
		r.Findings = append(r.Findings, Finding{Severity: SeverityLow, Title: strings.Repeat("x", 400), Detail: "d", Recommendation: "r"})
	}
	if n := len(brief(r)); n > maxBriefBytes {
		t.Errorf("brief is %d bytes, over the %d cap", n, maxBriefBytes)
	}
}

func TestExplainerFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if e, err := ExplainerFromEnv(env(nil)); e != nil || err != nil {
		t.Errorf("unset: got %v, %v; want off without an error", e, err)
	}
	if e, err := ExplainerFromEnv(env(map[string]string{EnvExplainProvider: "openai", EnvExplainAPIKey: "sk-plain"})); e != nil || err == nil {
		t.Errorf("a plaintext key must be refused: got %v, %v", e, err)
	}
	if e, err := ExplainerFromEnv(env(map[string]string{EnvExplainProvider: "openai", EnvExplainAPIKey: `{{secret("K")}}`})); e == nil || err != nil {
		t.Errorf("a secret reference must be accepted: got %v, %v", e, err)
	}
}

// A flow that has only just started has no window worth quoting; the brief
// must not hand the model "0 messages" as if it were a measurement.
func TestBriefSaysWhenTheWindowIsTooShortToMeasure(t *testing.T) {
	got := brief(Report{WorkflowID: "wf-1", State: StateCollecting, Findings: []Finding{{Severity: SeverityLow, Title: "t"}}})
	if strings.Contains(got, "0 messages") || !strings.Contains(got, "too short") {
		t.Errorf("brief = %q", got)
	}
}
