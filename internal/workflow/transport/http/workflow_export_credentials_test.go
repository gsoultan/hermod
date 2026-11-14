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
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/workflow/redact"
)

func (m *MockWorkflowStorage) UpdateSource(_ context.Context, src storage.Source) error {
	m.sources[src.ID] = src
	return nil
}

func (m *MockWorkflowStorage) UpdateSink(_ context.Context, snk storage.Sink) error {
	m.sinks[snk.ID] = snk
	return nil
}

func credentialFixture() (*MockWorkflowStorage, *http.ServeMux, *WorkflowHandler) {
	store := &MockWorkflowStorage{
		workflows: map[string]storage.Workflow{
			"wf-1": {ID: "wf-1", Name: "orders", Nodes: []storage.WorkflowNode{
				{ID: "n1", Type: "source", RefID: "src-1"},
				{ID: "n2", Type: "sink", RefID: "snk-1"},
			}},
		},
		sources: map[string]storage.Source{
			"src-1": {ID: "src-1", Name: "pg", Type: "postgres", VHost: "default", Config: map[string]string{
				"host": "db.internal", "user": "app", "password": "S3cret-src",
				"api_token": "{{secret.PG_TOKEN}}", "dsn": "",
			}},
		},
		sinks: map[string]storage.Sink{
			"snk-1": {ID: "snk-1", Name: "hook", Type: "http", VHost: "default", Config: map[string]string{
				"url": "https://bot:S3cret-url@hooks.example.com/in", "api_key": "S3cret-key",
			}},
		},
	}
	h := &WorkflowHandler{Handler: &handlers.Handler{Storage: store, LogStorage: store}}
	mux := http.NewServeMux()
	h.RegisterWorkflowRoutes(mux)
	return store, mux, h
}

func exportCredentialBundle(t *testing.T, mux *http.ServeMux) (storage.WorkflowExportBundle, string) {
	t.Helper()
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), "GET", "/api/workflows/wf-1/export", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("export: %d %s", rr.Code, rr.Body.String())
	}
	raw := rr.Body.String()
	var b storage.WorkflowExportBundle
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatal(err)
	}
	return b, raw
}

// An export file is downloaded, mailed and committed. The connection
// passwords of its sources and sinks stay on the instance that wrote it; the
// file says which ones it left out, and keeps everything else, references
// included.
func TestExportLeavesConnectionCredentialsOut(t *testing.T) {
	_, mux, _ := credentialFixture()
	b, raw := exportCredentialBundle(t, mux)

	for _, secret := range []string{"S3cret-src", "S3cret-url", "S3cret-key"} {
		if strings.Contains(raw, secret) {
			t.Errorf("the export file carries %q", secret)
		}
	}
	src := b.Sources[0].Config
	if src["password"] != redact.Placeholder {
		t.Errorf("source password = %q, want %q", src["password"], redact.Placeholder)
	}
	if src["host"] != "db.internal" || src["user"] != "app" {
		t.Errorf("non-credential settings changed: %v", src)
	}
	if src["api_token"] != "{{secret.PG_TOKEN}}" {
		t.Errorf("a secret reference is a name and must stay: %q", src["api_token"])
	}
	if src["dsn"] != "" {
		t.Errorf("an empty credential must stay empty, got %q", src["dsn"])
	}
	snk := b.Sinks[0].Config
	if snk["url"] != redact.Placeholder || snk["api_key"] != redact.Placeholder {
		t.Errorf("sink credentials not left out: %v", snk)
	}
}

// Importing the file back where it came from needs nothing typed: a left-out
// value is this instance's own value for the same connection.
func TestImportKeepsThisInstancesCredentialForALeftOutValue(t *testing.T) {
	store, mux, h := credentialFixture()
	b, _ := exportCredentialBundle(t, mux)
	body, _ := json.Marshal(b)

	rr := httptest.NewRecorder()
	h.ImportWorkflow(rr, httptest.NewRequestWithContext(t.Context(), "POST", "/api/workflows/import", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rr.Code, rr.Body.String())
	}
	if got := store.sources["src-1"].Config["password"]; got != "S3cret-src" {
		t.Errorf("source password after import = %q, want the stored one", got)
	}
	if got := store.sinks["snk-1"].Config["url"]; got != "https://bot:S3cret-url@hooks.example.com/in" {
		t.Errorf("sink url after import = %q, want the stored one", got)
	}
}

// Elsewhere the value has to be given. The import writes nothing and names
// every connection and setting it needs, so the wizard can ask for them.
func TestImportElsewhereAsksForEachLeftOutCredential(t *testing.T) {
	store, mux, h := credentialFixture()
	b, _ := exportCredentialBundle(t, mux)
	store.workflows = map[string]storage.Workflow{}
	store.sources = map[string]storage.Source{}
	store.sinks = map[string]storage.Sink{}
	body, _ := json.Marshal(b)

	rr := httptest.NewRecorder()
	h.ImportWorkflow(rr, httptest.NewRequestWithContext(t.Context(), "POST", "/api/workflows/import", bytes.NewReader(body)))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("import: %d %s, want 422", rr.Code, rr.Body.String())
	}
	var resp struct {
		Error   string               `json:"error"`
		Missing []withheldCredential `json:"missing_credentials"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"source/src-1/password": true, "sink/snk-1/api_key": true, "sink/snk-1/url": true}
	if len(resp.Missing) != len(want) {
		t.Fatalf("missing = %+v, want %v", resp.Missing, want)
	}
	for _, m := range resp.Missing {
		if !want[m.Kind+"/"+m.ID+"/"+m.Field] {
			t.Errorf("unexpected missing credential %+v", m)
		}
	}
	if len(store.sources)+len(store.sinks)+len(store.workflows) != 0 {
		t.Error("an import that is missing credentials must write nothing")
	}

	// Typed in, it imports.
	b.Sources[0].Config["password"] = "typed-pw"
	b.Sinks[0].Config["api_key"] = "typed-key"
	b.Sinks[0].Config["url"] = "https://hooks.example.com/in"
	body, _ = json.Marshal(b)
	rr = httptest.NewRecorder()
	h.ImportWorkflow(rr, httptest.NewRequestWithContext(t.Context(), "POST", "/api/workflows/import", bytes.NewReader(body)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("import with credentials: %d %s", rr.Code, rr.Body.String())
	}
	if store.sources["src-1"].Config["password"] != "typed-pw" {
		t.Errorf("typed password not saved: %v", store.sources["src-1"].Config)
	}
}
