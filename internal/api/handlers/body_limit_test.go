// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Most JSON handlers decode r.Body with no limit — login and the public form
// endpoint among them — so one request could make the server buffer as much
// as the client cared to send. BodyLimitMiddleware puts one ceiling on every
// request body. Handlers with a tighter limit of their own keep it; the one
// route that needs more (a dataset file upload) is exempt and keeps its own.

// decodeAll is a handler that, like the unguarded ones, decodes the whole body.
func decodeAll(w http.ResponseWriter, r *http.Request) {
	var v any
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// jsonOfSize is a JSON string literal n bytes long in total.
func jsonOfSize(n int) []byte {
	b := bytes.Repeat([]byte("a"), n)
	b[0], b[n-1] = '"', '"'
	return b
}

func TestBodyLimitMiddlewareCapsAnUnguardedHandler(t *testing.T) {
	h := &Handler{}
	srv := h.BodyLimitMiddlewareWithLimit(1024)(http.HandlerFunc(decodeAll))

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/login", bytes.NewReader(jsonOfSize(4096))))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a 4 KiB body past a 1 KiB limit was answered %d, want 413", rec.Code)
	}

	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/login", bytes.NewReader(jsonOfSize(1024))))
	if rec.Code != http.StatusOK {
		t.Errorf("a body at the limit was answered %d, want 200", rec.Code)
	}
}

func TestBodyLimitMiddlewareLeavesTheDatasetUploadToItsOwnLimit(t *testing.T) {
	h := &Handler{}
	var read int64
	srv := h.BodyLimitMiddlewareWithLimit(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read, _ = io.Copy(io.Discard, r.Body)
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/vhosts/default/ml/datasets/sales/file", bytes.NewReader(make([]byte, 8192)))
	srv.ServeHTTP(httptest.NewRecorder(), req)
	if read != 8192 {
		t.Errorf("the dataset upload read %d of 8192 bytes; its own 200 MiB limit should apply, not the default", read)
	}
}

func TestDefaultMaxRequestBodyIsAboveEveryHandlerOwnLimit(t *testing.T) {
	// The tightest-wins rule only holds if the default is not below a limit a
	// handler already chose: the predict endpoint takes 8 MiB and uploads 10.
	if got := maxRequestBodyBytes(); got < 10<<20 {
		t.Errorf("default request body limit is %d bytes, below the 10 MiB upload limit", got)
	}
}
