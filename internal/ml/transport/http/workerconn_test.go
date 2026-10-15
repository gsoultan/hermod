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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// ---------------------------------------------------------------------------
// Connecting the ML worker from the console: an administrator reads, tests
// and saves the worker's address and token; nobody else may, the token never
// comes back, and a worker the environment names stays as it is.
// ---------------------------------------------------------------------------

func (s *modelAPIStore) GetSetting(_ context.Context, key string) (string, error) {
	return s.settings[key], nil
}

func (s *modelAPIStore) SaveSetting(_ context.Context, key, value string) error {
	s.settings[key] = value
	return nil
}

// newConnectionAPI is the model API with its own worker provider; env is the
// worker HERMOD_ML_WORKER_URL names, or nil.
func newConnectionAPI(env *worker.Client) (*Handler, *modelAPIStore) {
	h, store := newModelAPI()
	h.workers = worker.NewProvider(func() *worker.Client { return env })
	return h, store
}

// fakeMLWorker answers like hermod-ml with token; deep adds the networks.
func fakeMLWorker(t *testing.T, token string, deep bool) string {
	t.Helper()
	algorithms := `["auto","random_forest"]`
	if deep {
		algorithms = `["auto","random_forest","pytorch_mlp","keras_mlp"]`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/health/ready":
			_, _ = io.WriteString(w, `{"ready":true}`)
		case "/v1/capabilities":
			if r.Header.Get("Authorization") != "Bearer "+token {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"error":"Missing or invalid bearer token."}`)
				return
			}
			_, _ = io.WriteString(w, `{"tasks":["auto"],"algorithms":`+algorithms+`,"unavailable":{}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

type connectionReply struct {
	Source   string `json:"source"`
	URL      string `json:"url"`
	TokenSet bool   `json:"token_set"`
	Editable bool   `json:"editable"`
}

func decodeConnection(t *testing.T, w *httptest.ResponseRecorder) connectionReply {
	t.Helper()
	var c connectionReply
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatalf("reply %d %s: %v", w.Code, w.Body, err)
	}
	return c
}

func TestWorkerConnectionIsForAdministratorsOnly(t *testing.T) {
	h, _ := newConnectionAPI(nil)
	for _, u := range []*storage.User{editorA, viewerA, nil} {
		for _, c := range []struct {
			fn     http.HandlerFunc
			method string
			body   string
		}{
			{h.GetWorkerConnection, http.MethodGet, ""},
			{h.PutWorkerConnection, http.MethodPut, `{"url":"http://a:8090","token":"t"}`},
			{h.CheckWorkerConnection, http.MethodPost, `{"url":"http://a:8090","token":"t"}`},
		} {
			w := do(c.fn, u, c.method, "/api/ml/worker/connection", strings.NewReader(c.body), nil)
			if w.Code != http.StatusForbidden {
				t.Errorf("%s as %v = %d, want 403", c.method, u, w.Code)
			}
		}
	}
	if c, _ := h.workers.Current(); c != nil {
		t.Errorf("a refused save connected %+v", c)
	}
}

func TestWorkerConnectionSavesWithoutEverReturningTheToken(t *testing.T) {
	h, store := newConnectionAPI(nil)
	url := fakeMLWorker(t, "s3cret", false)

	w := do(h.GetWorkerConnection, admin, http.MethodGet, "/x", nil, nil)
	if c := decodeConnection(t, w); c.Source != "none" || !c.Editable || c.TokenSet {
		t.Fatalf("before = %+v", c)
	}

	w = do(h.PutWorkerConnection, admin, http.MethodPut, "/x", strings.NewReader(`{"url":"`+url+`/","token":"s3cret"}`), nil)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "s3cret") {
		t.Fatalf("save = %d %s", w.Code, w.Body)
	}
	if c := decodeConnection(t, w); c.Source != "saved" || c.URL != url || !c.TokenSet || !c.Editable {
		t.Errorf("after save = %+v", c)
	}
	if strings.Contains(store.settings[storage.MLWorkerSettingKey], "s3cret") {
		t.Errorf("stored the token in the clear: %s", store.settings[storage.MLWorkerSettingKey])
	}
	w = do(h.GetWorkerConnection, admin, http.MethodGet, "/x", nil, nil)
	if strings.Contains(w.Body.String(), "s3cret") || !decodeConnection(t, w).TokenSet {
		t.Errorf("read back = %s", w.Body)
	}
	if !strings.Contains(strings.Join(auditMessages(store), "|"), "ml_worker_url") {
		t.Errorf("saving the connection was not audited")
	}
	for _, a := range store.audits {
		if strings.Contains(a.Payload, "s3cret") {
			t.Errorf("the audit log holds the token: %s", a.Payload)
		}
	}

	// In force at once: the status endpoint, and training, reach it.
	w = do(h.WorkerStatus, viewerA, http.MethodGet, "/api/ml/worker", nil, nil)
	if !strings.Contains(w.Body.String(), `"ready":true`) || !strings.Contains(w.Body.String(), `"source":"saved"`) {
		t.Errorf("status after save = %s", w.Body)
	}
}

func auditMessages(s *modelAPIStore) []string {
	var out []string
	for _, a := range s.audits {
		out = append(out, a.Action+" "+a.Payload)
	}
	return out
}

func TestWorkerConnectionRefusesABadAddress(t *testing.T) {
	h, _ := newConnectionAPI(nil)
	for _, body := range []string{`{"url":"hermod-ml:8090"}`, `{"url":"http://u:p@x:8090"}`, `not json`} {
		w := do(h.PutWorkerConnection, admin, http.MethodPut, "/x", strings.NewReader(body), nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", body, w.Code, w.Body)
		}
	}
}

func TestWorkerConnectionFromTheEnvironmentIsReadOnly(t *testing.T) {
	h, _ := newConnectionAPI(worker.New("http://env-worker:8090", "env-token", nil))
	w := do(h.GetWorkerConnection, admin, http.MethodGet, "/x", nil, nil)
	c := decodeConnection(t, w)
	if c.Source != "environment" || c.Editable || c.URL != "http://env-worker:8090" || !c.TokenSet {
		t.Errorf("env connection = %+v", c)
	}
	if strings.Contains(w.Body.String(), "env-token") {
		t.Errorf("returned the environment's token: %s", w.Body)
	}
	w = do(h.PutWorkerConnection, admin, http.MethodPut, "/x", strings.NewReader(`{"url":"http://a:8090","token":"t"}`), nil)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "environment") {
		t.Errorf("save over env = %d %s, want 409", w.Code, w.Body)
	}
	if cur, _ := h.workers.Current(); cur.URL != "http://env-worker:8090" {
		t.Errorf("worker in force = %s", cur.URL)
	}
}

func TestCheckWorkerConnectionSaysWhatIsWrong(t *testing.T) {
	h, _ := newConnectionAPI(nil)
	url := fakeMLWorker(t, "s3cret", true)
	tests := []struct {
		name, body, result string
		deep               bool
	}{
		{"connected with deep learning", `{"url":"` + url + `","token":"s3cret"}`, "ok", true},
		{"wrong token", `{"url":"` + url + `","token":"nope"}`, "unauthorized", false},
		{"wrong address", `{"url":"http://127.0.0.1:1"}`, "unreachable", false},
		{"not a URL", `{"url":"hermod-ml"}`, "bad_address", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := do(h.CheckWorkerConnection, admin, http.MethodPost, "/x", strings.NewReader(tt.body), nil)
			// 200 whatever the worker said: a 401 here would sign the
			// administrator out of Hermod.
			if w.Code != http.StatusOK {
				t.Fatalf("check = %d %s", w.Code, w.Body)
			}
			var r worker.ProbeReport
			_ = json.Unmarshal(w.Body.Bytes(), &r)
			if r.Result != worker.ProbeResult(tt.result) || r.DeepLearning != tt.deep || r.Message == "" {
				t.Errorf("report = %+v", r)
			}
			if strings.Contains(w.Body.String(), "s3cret") {
				t.Errorf("echoed the token: %s", w.Body)
			}
		})
	}
	if c, _ := h.workers.Current(); c != nil {
		t.Errorf("a test connected %+v", c)
	}
}

// Testing a changed address without typing the token again uses the saved one.
func TestCheckWorkerConnectionUsesTheSavedTokenWhenNoneIsGiven(t *testing.T) {
	h, _ := newConnectionAPI(nil)
	url := fakeMLWorker(t, "s3cret", false)
	if w := do(h.PutWorkerConnection, admin, http.MethodPut, "/x", strings.NewReader(`{"url":"http://old:8090","token":"s3cret"}`), nil); w.Code != http.StatusOK {
		t.Fatalf("save = %d %s", w.Code, w.Body)
	}
	w := do(h.CheckWorkerConnection, admin, http.MethodPost, "/x", strings.NewReader(`{"url":"`+url+`"}`), nil)
	if !strings.Contains(w.Body.String(), `"result":"ok"`) {
		t.Errorf("check = %s", w.Body)
	}
}
