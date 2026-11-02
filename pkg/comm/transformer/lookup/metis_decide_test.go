// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package lookup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
)

// fakeMetis answers the decision and login routes of a Metis server and
// records what it was asked.
type fakeMetis struct {
	srv *httptest.Server

	mu       sync.Mutex
	requests []map[string]any
	auths    []string
	orgs     []string

	logins       atomic.Int32
	evaluations  atomic.Int32
	status       int    // the evaluate route's status; 0 is 200
	reply        string // the evaluate route's body
	rejectTokens map[string]bool
}

const approvalTierReply = `{"result":{
  "values":{"tier":"manager","limit":5000},
  "matched_rules":[2],
  "matched_rule_ids":["r-3"],
  "decision_key":"approval-tier",
  "decision_name":"Approval tier",
  "decision_version":7}}`

const noMatchReply = `{"result":{"values":{},"decision_key":"approval-tier","decision_version":7}}`

func newFakeMetis(t *testing.T) *fakeMetis {
	t.Helper()
	f := &fakeMetis{reply: approvalTierReply, rejectTokens: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/login":
			n := f.logins.Add(1)
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["username"] != "svc" || body["password"] != "pw" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"bad credentials"}`))
				return
			}
			_, _ = w.Write([]byte(`{"token":"login-` + strconv.Itoa(int(n)) + `"}`))
		case "/api/v1/decisions/evaluate":
			f.evaluations.Add(1)
			auth := r.Header.Get("Authorization")
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.requests = append(f.requests, body)
			f.auths = append(f.auths, auth)
			f.orgs = append(f.orgs, r.Header.Get("X-Organization-ID"))
			reject := f.rejectTokens[strings.TrimPrefix(auth, "Bearer ")]
			status, reply := f.status, f.reply
			f.mu.Unlock()
			if reject {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"token expired"}`))
				return
			}
			if status != 0 {
				w.WriteHeader(status)
			}
			_, _ = w.Write([]byte(reply))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeMetis) lastRequest(t *testing.T) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		t.Fatal("Metis was not asked to evaluate anything")
	}
	return f.requests[len(f.requests)-1]
}

func (f *fakeMetis) config(extra map[string]any) map[string]any {
	cfg := map[string]any{
		"url":         f.srv.URL,
		"token":       "tok",
		"projectId":   "proj-1",
		"decisionKey": "approval-tier",
	}
	for k, v := range extra {
		cfg[k] = v
	}
	return cfg
}

func runMetisDecide(t *testing.T, tr transformer.Transformer, cfg, fields map[string]any) (hermod.Message, error) {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	for k, v := range fields {
		msg.SetData(k, v)
	}
	return tr.Transform(context.Background(), msg, cfg)
}

func orderFields() map[string]any {
	return map[string]any{
		"amount":   900,
		"customer": map[string]any{"tier": "gold", "score": 712},
	}
}

func TestMetisDecideIsRegistered(t *testing.T) {
	if _, ok := transformer.Get("metis_decide"); !ok {
		t.Fatal("metis_decide is not registered")
	}
}

func TestMetisDecideEvaluatesTheWholeRecordAndWritesTheResult(t *testing.T) {
	f := newFakeMetis(t)
	out, err := runMetisDecide(t, NewMetisDecideTransformer(), f.config(nil), orderFields())
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}

	req := f.lastRequest(t)
	if req["project_id"] != "proj-1" || req["key"] != "approval-tier" {
		t.Errorf("asked project %v, key %v", req["project_id"], req["key"])
	}
	if _, pinned := req["version"]; pinned {
		t.Errorf("a version was sent although none is pinned: %v", req["version"])
	}
	vars, _ := req["variables"].(map[string]any)
	if vars["amount"] != float64(900) {
		t.Errorf("variables = %v, want the whole record", vars)
	}
	if f.auths[0] != "Bearer tok" {
		t.Errorf("Authorization = %q", f.auths[0])
	}

	got, _ := out.Data()["decision"].(map[string]any)
	if got == nil {
		t.Fatalf("no decision field in %v", out.Data())
	}
	values, _ := got["values"].(map[string]any)
	if values["tier"] != "manager" {
		t.Errorf("values = %v", values)
	}
	if got["matched"] != true {
		t.Errorf("matched = %v", got["matched"])
	}
	if ids, _ := got["matched_rule_ids"].([]any); len(ids) != 1 || ids[0] != "r-3" {
		t.Errorf("matched_rule_ids = %#v", got["matched_rule_ids"])
	}
	decision, _ := got["decision"].(map[string]any)
	if decision["key"] != "approval-tier" || decision["version"] != 7 {
		t.Errorf("decision = %v", decision)
	}
	if _, merged := out.Data()["tier"]; merged {
		t.Error("outputs were copied to the top level without mergeOutputs")
	}
}

func TestMetisDecideSendsOnlyTheMappedInputs(t *testing.T) {
	f := newFakeMetis(t)
	cfg := f.config(map[string]any{
		"inputs": []any{
			map[string]any{"name": "score", "field": "customer.score"},
			map[string]any{"name": "amount", "field": "amount"},
			map[string]any{"name": "", "field": "ignored"},
		},
	})
	if _, err := runMetisDecide(t, NewMetisDecideTransformer(), cfg, orderFields()); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	vars, _ := f.lastRequest(t)["variables"].(map[string]any)
	if len(vars) != 2 || vars["score"] != float64(712) || vars["amount"] != float64(900) {
		t.Errorf("variables = %v, want score and amount only", vars)
	}
}

func TestMetisDecidePinsTheVersionAndChoosesTheTableFromTheRecord(t *testing.T) {
	f := newFakeMetis(t)
	cfg := f.config(map[string]any{"decisionKey": "{{.table}}", "version": "3", "organizationId": "org-9"})
	fields := orderFields()
	fields["table"] = "discount"
	if _, err := runMetisDecide(t, NewMetisDecideTransformer(), cfg, fields); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	req := f.lastRequest(t)
	if req["key"] != "discount" {
		t.Errorf("key = %v, want the one the record names", req["key"])
	}
	if req["version"] != float64(3) {
		t.Errorf("version = %v, want 3", req["version"])
	}
	if f.orgs[0] != "org-9" {
		t.Errorf("X-Organization-ID = %q", f.orgs[0])
	}
}

func TestMetisDecideMergesOutputsWhenAsked(t *testing.T) {
	f := newFakeMetis(t)
	out, err := runMetisDecide(t, NewMetisDecideTransformer(),
		f.config(map[string]any{"mergeOutputs": true, "targetField": "policy"}), orderFields())
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if out.Data()["tier"] != "manager" {
		t.Errorf("tier = %v, want the output copied to the top level", out.Data()["tier"])
	}
	if _, ok := out.Data()["policy"].(map[string]any); !ok {
		t.Errorf("the result is not in the target field: %v", out.Data())
	}
}

func TestMetisDecideNoMatch(t *testing.T) {
	cases := []struct {
		onNoMatch string
		wantDrop  bool
		wantErr   bool
	}{
		{onNoMatch: "", wantDrop: false},
		{onNoMatch: "keep", wantDrop: false},
		{onNoMatch: "drop", wantDrop: true},
		{onNoMatch: "fail", wantErr: true},
	}
	for _, tc := range cases {
		t.Run("onNoMatch="+tc.onNoMatch, func(t *testing.T) {
			f := newFakeMetis(t)
			f.reply = noMatchReply
			out, err := runMetisDecide(t, NewMetisDecideTransformer(),
				f.config(map[string]any{"onNoMatch": tc.onNoMatch}), orderFields())
			switch {
			case tc.wantErr:
				if err == nil {
					t.Fatal("no error for a record no rule matched")
				}
				if !errors.Is(err, hermod.ErrPermanent) {
					t.Errorf("a no-match failure is retried: %v", err)
				}
			case tc.wantDrop:
				if err != nil || out != nil {
					t.Fatalf("out = %v, err = %v; want the record dropped", out, err)
				}
			default:
				if err != nil {
					t.Fatalf("Transform: %v", err)
				}
				got, _ := out.Data()["decision"].(map[string]any)
				if got["matched"] != false {
					t.Errorf("matched = %v", got["matched"])
				}
			}
		})
	}
}

func TestMetisDecideRefusalIsPermanentButAServerErrorIsNot(t *testing.T) {
	f := newFakeMetis(t)
	f.status, f.reply = http.StatusBadRequest, `{"error":"input score is missing"}`
	_, err := runMetisDecide(t, NewMetisDecideTransformer(), f.config(nil), orderFields())
	if err == nil || !errors.Is(err, hermod.ErrPermanent) {
		t.Fatalf("a 400 should be a permanent error, got %v", err)
	}
	if !strings.Contains(err.Error(), "input score is missing") {
		t.Errorf("the error drops Metis's reason: %v", err)
	}

	f.status, f.reply = http.StatusBadGateway, `{"error":"upstream"}`
	_, err = runMetisDecide(t, NewMetisDecideTransformer(), f.config(nil), orderFields())
	if err == nil || errors.Is(err, hermod.ErrPermanent) {
		t.Fatalf("a 502 should be retryable, got %v", err)
	}
}

func TestMetisDecideLogsInOnceAndAgainWhenTheTokenExpires(t *testing.T) {
	f := newFakeMetis(t)
	tr := NewMetisDecideTransformer()
	cfg := f.config(map[string]any{"token": "", "username": "svc", "password": "pw"})

	for range 3 {
		if _, err := runMetisDecide(t, tr, cfg, orderFields()); err != nil {
			t.Fatalf("Transform: %v", err)
		}
	}
	if n := f.logins.Load(); n != 1 {
		t.Fatalf("logged in %d times for three records, want once", n)
	}

	f.mu.Lock()
	f.rejectTokens["login-1"] = true
	f.mu.Unlock()
	if _, err := runMetisDecide(t, tr, cfg, orderFields()); err != nil {
		t.Fatalf("Transform after the token expired: %v", err)
	}
	if n := f.logins.Load(); n != 2 {
		t.Fatalf("logins = %d, want a second one after the token expired", n)
	}
	if last := f.auths[len(f.auths)-1]; last != "Bearer login-2" {
		t.Errorf("retried with %q, want the new token", last)
	}
}

func TestMetisDecideRefusesBadSettingsWithoutCallingMetis(t *testing.T) {
	f := newFakeMetis(t)
	cases := map[string]map[string]any{
		"no url":                 {"url": ""},
		"no credentials":         {"token": ""},
		"no project":             {"projectId": ""},
		"no decision key":        {"decisionKey": ""},
		"token from the record":  {"token": "{{.secret_token}}"},
		"url from the record":    {"url": "{{.host}}"},
		"plaintext off loopback": {"url": "http://metis.example.com"},
		"bad version":            {"version": "latest"},
		"bad timeout":            {"timeout": "10"},
		"bad onNoMatch":          {"onNoMatch": "ignore"},
	}
	for name, patch := range cases {
		t.Run(name, func(t *testing.T) {
			fields := orderFields()
			fields["secret_token"] = "stolen"
			fields["host"] = f.srv.URL
			_, err := runMetisDecide(t, NewMetisDecideTransformer(), f.config(patch), fields)
			if err == nil {
				t.Fatal("no error")
			}
			if !errors.Is(err, hermod.ErrPermanent) {
				t.Errorf("a settings error is retried: %v", err)
			}
			if !strings.HasPrefix(err.Error(), "metis_decide: ") {
				t.Errorf("error does not name the node: %v", err)
			}
		})
	}
	if n := f.evaluations.Load(); n != 0 {
		t.Errorf("Metis was called %d times for refused settings", n)
	}
}
