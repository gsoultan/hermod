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
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/builtin"
	"github.com/gsoultan/hermod/pkg/ml/profile"
)

func TestUploadSendsAParquetFileOn(t *testing.T) {
	h, _, stub := newTrainingAPI(t)
	vals := map[string]string{"vhost": "tenant-a", "name": "customers"}
	w := do(h.UploadDataset, editorA, http.MethodPut, "/x?format=parquet", strings.NewReader("PAR1…PAR1"), vals)
	if w.Code != http.StatusOK || stub.uploaded != "PAR1…PAR1" {
		t.Fatalf("upload = %d %s, worker got %q", w.Code, w.Body, stub.uploaded)
	}
	if !slices.ContainsFunc(stub.calls, func(c string) bool { return strings.HasSuffix(c, "/file?format=parquet") }) {
		t.Errorf("calls = %v, want the file sent as parquet", stub.calls)
	}
	w = do(h.UploadDataset, editorA, http.MethodPut, "/x?format=json", strings.NewReader("[]"), vals)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Parquet") {
		t.Errorf("a JSON upload: %d %s, want 400 naming the formats", w.Code, w.Body)
	}
}

// From an uploaded Parquet file to its profile, through the engine Hermod
// runs by default.
func TestProfileDescribesAnUploadedDataset(t *testing.T) {
	h, _ := newModelAPI()
	e, err := builtin.New(builtin.Config{Dir: t.TempDir(), Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	h.worker = e.Client()
	raw, err := os.ReadFile("../../../../pkg/ml/builtin/table/testdata/flat.parquet")
	if err != nil {
		t.Fatal(err)
	}
	vals := map[string]string{"vhost": "tenant-a", "name": "orders"}
	if w := do(h.UploadDataset, editorA, http.MethodPut, "/x?format=parquet", bytes.NewReader(raw), vals); w.Code != http.StatusOK {
		t.Fatalf("upload = %d %s", w.Code, w.Body)
	}

	w := do(h.ProfileDataset, editorA, http.MethodGet, "/x", nil, vals)
	if w.Code != http.StatusOK {
		t.Fatalf("profile = %d %s", w.Code, w.Body)
	}
	var p profile.Profile
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Dataset != "orders" || p.Rows != 3 || p.ColumnCount != 11 || p.Sampled {
		t.Errorf("profile = %+v", p)
	}
	kinds := map[string]string{}
	for _, c := range p.Columns {
		kinds[c.Name] = c.Kind
	}
	for name, want := range map[string]string{"qty": profile.KindNumber, "ok": profile.KindYesNo, "day": profile.KindDate, "plan": profile.KindCategory, "empty": profile.KindEmpty} {
		if kinds[name] != want {
			t.Errorf("%s is %q, want %q", name, kinds[name], want)
		}
	}
	if !slices.ContainsFunc(p.Warnings, func(w profile.Warning) bool { return w.Code == profile.CodeFewRows }) {
		t.Errorf("3 rows and no warning: %+v", p.Warnings)
	}
}

func TestProfileNeedsTheEditorRoleInItsVHost(t *testing.T) {
	h, _, _ := newTrainingAPI(t)
	vals := map[string]string{"vhost": "tenant-a", "name": "customers"}
	for _, tc := range []struct {
		name string
		who  *storage.User
	}{{"a viewer", viewerA}, {"another vhost's editor", editorB}} {
		if w := do(h.ProfileDataset, tc.who, http.MethodGet, "/x", nil, vals); w.Code != http.StatusForbidden {
			t.Errorf("%s profiled a dataset: %d %s", tc.name, w.Code, w.Body)
		}
	}
	// The stub has no such dataset, and no sample route at all.
	if w := do(h.ProfileDataset, editorA, http.MethodGet, "/x", nil, map[string]string{"vhost": "tenant-a", "name": "missing"}); w.Code != http.StatusNotFound {
		t.Errorf("a missing dataset: %d %s", w.Code, w.Body)
	}
}

func TestProfileOnAWorkerOlderThanAnalysisSaysToUpdateIt(t *testing.T) {
	h, _, _ := newTrainingAPI(t)
	// The stub has customers, and no sample route: a worker from before it.
	w := do(h.ProfileDataset, editorA, http.MethodGet, "/x", nil, map[string]string{"vhost": "tenant-a", "name": "customers"})
	if w.Code != http.StatusNotImplemented || !strings.Contains(w.Body.String(), "update it") {
		t.Errorf("an older worker: %d %s, want 501 saying to update it", w.Code, w.Body)
	}
}
