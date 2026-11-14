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
)

type recordingSinkStorage struct {
	fakeSinkStorage
	created []storage.Sink
}

func (r *recordingSinkStorage) CreateSink(_ context.Context, s storage.Sink) error {
	r.created = append(r.created, s)
	return nil
}

func txsqlSinkBody(t *testing.T, statements string) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(storage.Sink{Name: "orders", Type: "txsql", VHost: "/", Config: map[string]string{
		"driver": "postgres", "host": "db", "statements": statements,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b)
}

// A Transactional SQL sink whose statements cannot run is refused when it is
// saved, with the reason, rather than saved and then refused when its
// workflow starts.
func TestSavingATransactionalSQLSinkChecksItsStatements(t *testing.T) {
	store := &recordingSinkStorage{}
	h := &SinkHandler{Handler: &handlers.Handler{Storage: store}}
	mux := http.NewServeMux()
	h.RegisterSinkRoutes(mux)

	bad := `[{"name":"a","sql":"UPDATE t SET x = {{b.id}}"},{"name":"b","sql":"INSERT INTO t (y) VALUES (1) RETURNING id"}]`

	for _, req := range []*http.Request{
		httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/sinks", txsqlSinkBody(t, bad)),
		httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/sinks/sink-1", txsqlSinkBody(t, bad)),
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, asAdministrator(req))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s %s = %d, want 400; body %s", req.Method, req.URL.Path, rec.Code, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), "runs later") {
			t.Errorf("%s body %q does not give the reason", req.Method, rec.Body)
		}
	}
	if len(store.created) != 0 {
		t.Fatalf("a refused sink was stored: %+v", store.created)
	}

	good := storage.Sink{Type: "txsql", Config: map[string]string{
		"driver": "postgres", "host": "db",
		"statements": `[{"name":"a","sql":"INSERT INTO t (y) VALUES (1) RETURNING id"},{"name":"b","sql":"UPDATE t SET x = {{a.id}}"}]`,
	}}
	if err := checkSinkConfig(good); err != nil {
		t.Fatalf("a valid sink was refused: %v", err)
	}
	// Other sink types are not judged here.
	if err := checkSinkConfig(storage.Sink{Type: "postgres", Config: map[string]string{}}); err != nil {
		t.Fatalf("a postgres sink was refused: %v", err)
	}
}
