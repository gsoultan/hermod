// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/advisor"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/selfheal"
	"github.com/gsoultan/hermod/internal/storage"
)

// fakeInsights stands in for the registry's advisor.
type fakeInsights struct {
	report advisor.Report
	since  advisor.Summary
	ok     bool
}

func (f *fakeInsights) WorkflowInsights(context.Context, string) advisor.Report { return f.report }
func (f *fakeInsights) WorkflowSummarySince(context.Context, string, time.Time) (advisor.Summary, bool) {
	return f.since, f.ok
}

func lookupOrders() storage.Workflow {
	wf := orders()
	wf.Nodes = append(wf.Nodes, storage.WorkflowNode{ID: "lookup", Type: "transformation",
		Config: map[string]any{"transType": "api_lookup", "ttl": "0", "url": "http://x", "targetField": "c"}})
	return wf
}

type fixFixture struct {
	fixture
	ins *fakeInsights
}

func newFixFixture(t *testing.T) fixFixture {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db_config.yaml"),
		[]byte("type: sqlite\nconn: \"file::memory:\"\njwt_secret: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMOD_CONFIG_DIR", dir)
	st := &store{settings: map[string]string{}, workflows: map[string]storage.Workflow{"wf": lookupOrders()}}
	h := &handlers.Handler{Storage: st, LogStorage: st}
	ph := NewProposalHandler(h)
	ins := &fakeInsights{report: advisor.Report{WorkflowID: "wf", State: advisor.StateReady,
		Summary: &advisor.Summary{Throughput: 40, StepTimeMs: 40, Messages: 7000, WindowSeconds: 180},
		Findings: []advisor.Finding{
			{Rule: advisor.RuleLookupCacheOff, NodeID: "lookup", Fix: advisor.FixLookupCache, Detail: "Find customer's ttl is 0."},
			{Rule: advisor.RuleFilterEarlier, NodeID: "n2", Detail: "moved late"},
		}}}
	ph.insights = ins
	mux := http.NewServeMux()
	ph.RegisterProposalRoutes(mux)
	return fixFixture{fixture: fixture{st: st, mux: mux, svc: ph.Service()}, ins: ins}
}

func (f fixFixture) post(t *testing.T, user *storage.User, body string) *httptest.ResponseRecorder {
	t.Helper()
	ctx := context.WithValue(t.Context(), handlers.UserContextKey, user)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/workflows/wf/proposals", strings.NewReader(body)))
	return rec
}

func TestARecommendationBecomesAPendingProposal(t *testing.T) {
	f := newFixFixture(t)
	rec := f.post(t, editorA, `{"rule":"lookup_cache_off","node_id":"lookup"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var p selfheal.Proposal
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Status != selfheal.StatusPending || p.Kind != selfheal.KindLookupCache || p.Reason != "Find customer's ttl is 0." {
		t.Fatalf("proposal = %+v", p)
	}
	if wf := f.st.workflows["wf"]; wf.Nodes[2].Config["ttl"] != "0" {
		t.Fatal("asking for a fix changed the workflow before anyone approved it")
	}
}

func TestOnlyAFindingTheAdvisorReportsCanBeProposed(t *testing.T) {
	f := newFixFixture(t)
	for body, want := range map[string]int{
		// Not in the report: the caller cannot invent a finding.
		`{"rule":"trace_sampling"}`: http.StatusConflict,
		// In the report, but only a person can move a step.
		`{"rule":"filter_earlier","node_id":"n2"}`:   http.StatusConflict,
		`{"rule":"lookup_cache_off","node_id":"n1"}`: http.StatusConflict,
		`not json`: http.StatusBadRequest,
		// A patch in the body is ignored, never applied: unknown fields are refused.
		`{"rule":"lookup_cache_off","node_id":"lookup","patch":[{"op":"replace","path":"/name","after":"x"}]}`: http.StatusBadRequest,
	} {
		if rec := f.post(t, editorA, body); rec.Code != want {
			t.Errorf("%s: status %d, want %d (%s)", body, rec.Code, want, rec.Body)
		}
	}
}

func TestProposingAFixNeedsAnEditorOfTheVHost(t *testing.T) {
	f := newFixFixture(t)
	body := `{"rule":"lookup_cache_off","node_id":"lookup"}`
	if rec := f.post(t, viewerA, body); rec.Code != http.StatusForbidden {
		t.Errorf("viewer: status %d, want 403", rec.Code)
	}
	if rec := f.post(t, editorB, body); rec.Code != http.StatusForbidden {
		t.Errorf("another vhost's editor: status %d, want 403", rec.Code)
	}
}

func TestAnAppliedFixReportsItsEffect(t *testing.T) {
	f := newFixFixture(t)
	rec := f.post(t, editorA, `{"rule":"lookup_cache_off","node_id":"lookup"}`)
	var p selfheal.Proposal
	_ = json.Unmarshal(rec.Body.Bytes(), &p)

	if rec := f.do(t, editorA, http.MethodPost, "/api/workflows/wf/proposals/"+p.ID+"/approve"); rec.Code != http.StatusOK {
		t.Fatalf("approve %d: %s", rec.Code, rec.Body)
	}

	type view struct {
		selfheal.Proposal
		Effect        *advisor.Effect `json:"effect"`
		EffectPending bool            `json:"effect_pending"`
	}
	list := func() view {
		rec := f.do(t, viewerA, http.MethodGet, "/api/workflows/wf/proposals")
		var body struct {
			Data []view `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Data) != 1 {
			t.Fatalf("list: %v %s", err, rec.Body)
		}
		return body.Data[0]
	}

	// Too soon after the change to say.
	got := list()
	if got.Baseline == nil || got.Baseline.StepTimeMs != 40 {
		t.Fatalf("baseline = %+v", got.Baseline)
	}
	if got.Effect != nil || !got.EffectPending {
		t.Fatalf("effect = %+v pending = %v before there are readings", got.Effect, got.EffectPending)
	}

	f.ins.since, f.ins.ok = advisor.Summary{Throughput: 40, StepTimeMs: 4, Messages: 7000, WindowSeconds: 180}, true
	got = list()
	if got.Effect == nil || got.Effect.Verdict != advisor.VerdictBetter || got.Effect.Before.StepTimeMs != 40 || got.Effect.After.StepTimeMs != 4 {
		t.Fatalf("effect = %+v", got.Effect)
	}
	if got.EffectPending {
		t.Fatal("still pending after the effect was measured")
	}
}
