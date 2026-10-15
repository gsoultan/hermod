// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package http

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/ml"
)

// The query builder's routes: a source's tables and columns, and a preview of
// the query. Each names a source by id and is an editor's, like filling a
// dataset from a query.
func TestQueryBuilderRoutesNeedAnEditorTheEngineAndTheirInputs(t *testing.T) {
	h, _, _ := newTrainingAPI(t)
	vals := map[string]string{"vhost": "tenant-a", "id": "shop"}

	for name, call := range map[string]func() *httptest.ResponseRecorder{
		"tables": func() *httptest.ResponseRecorder { return do(h.SourceTables, viewerA, http.MethodGet, "/x", nil, vals) },
		"columns": func() *httptest.ResponseRecorder {
			return do(h.SourceColumns, viewerA, http.MethodGet, "/x?table=orders", nil, vals)
		},
		"preview": func() *httptest.ResponseRecorder {
			return do(h.PreviewQuery, viewerA, http.MethodPost, "/x", strings.NewReader(`{"query":"SELECT 1"}`), vals)
		},
	} {
		if w := call(); w.Code != http.StatusForbidden {
			t.Errorf("%s by a viewer = %d, want 403", name, w.Code)
		}
	}
	if w := do(h.SourceTables, editorB, http.MethodGet, "/x", nil, vals); w.Code != http.StatusForbidden {
		t.Errorf("another vhost's editor listed tables: %d", w.Code)
	}

	if w := do(h.SourceColumns, editorA, http.MethodGet, "/x", nil, vals); w.Code != http.StatusBadRequest {
		t.Errorf("columns without a table = %d %s, want 400", w.Code, w.Body)
	}
	if w := do(h.PreviewQuery, editorA, http.MethodPost, "/x", strings.NewReader(`{"query":"  "}`), vals); w.Code != http.StatusBadRequest {
		t.Errorf("preview without a query = %d %s, want 400", w.Code, w.Body)
	}

	for name, w := range map[string]*httptest.ResponseRecorder{
		"tables":  do(h.SourceTables, editorA, http.MethodGet, "/x", nil, vals),
		"columns": do(h.SourceColumns, editorA, http.MethodGet, "/x?table=orders", nil, vals),
		"preview": do(h.PreviewQuery, editorA, http.MethodPost, "/x", strings.NewReader(`{"query":"SELECT 1"}`), vals),
	} {
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s without the engine = %d %s, want 503", name, w.Code, w.Body)
		}
	}
}

func TestAMissingSourceOrTableIsNotFound(t *testing.T) {
	h, _, _ := newTrainingAPI(t)
	for _, err := range []error{
		fmt.Errorf("source %q does not belong to vhost %q: %w", "theirs", "tenant-a", ml.ErrSourceNotFound),
		fmt.Errorf("%q: %w", "customers", ml.ErrSourceTableNotFound),
	} {
		w := httptest.NewRecorder()
		h.fail(w, err)
		if w.Code != http.StatusNotFound {
			t.Errorf("%v = %d, want 404", err, w.Code)
		}
	}
}
