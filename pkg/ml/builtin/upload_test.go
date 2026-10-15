// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package builtin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestUploadParquetKeepsItsColumnTypes(t *testing.T) {
	_, c := newEngine(t)
	raw, err := os.ReadFile("table/testdata/flat.parquet")
	if err != nil {
		t.Fatal(err)
	}
	info, err := c.UploadFile(context.Background(), "v", "orders", "parquet", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if info.Rows != 3 {
		t.Errorf("rows = %d, want 3", info.Rows)
	}
	types := map[string]string{}
	for _, col := range info.Columns {
		types[col.Name] = col.Type
	}
	for name, want := range map[string]string{"qty": "number", "amount": "number", "ok": "bool", "sku": "string", "day": "string"} {
		if types[name] != want {
			t.Errorf("column %s is %q, want %q", name, types[name], want)
		}
	}
}

func TestUploadRefusesAFormatOrFileItCannotRead(t *testing.T) {
	e, _ := newEngine(t)
	for _, tc := range []struct {
		name, format, body, want string
	}{
		{"unknown format", "json", "[]", "'csv', 'xlsx' or 'parquet'"},
		{"not parquet", "parquet", "a,b\n1,2\n", "could not read the parquet file"},
		{"empty parquet", "parquet", "", "the parquet file is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/v1/datasets/v/d/file?format="+tc.format, strings.NewReader(tc.body))
			e.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest || !strings.Contains(strings.ToLower(rec.Body.String()), strings.ToLower(tc.want)) {
				t.Fatalf("answered %d %s, want 400 saying %q", rec.Code, rec.Body, tc.want)
			}
		})
	}
}
