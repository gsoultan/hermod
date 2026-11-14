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
	"net/http/httptest"
	"testing"
)

// ?limit= goes straight into the SQL LIMIT of every list endpoint, so an
// uncapped value lets one request read a whole table into memory.
func TestListPageSizeIsCapped(t *testing.T) {
	h := &Handler{}
	cases := map[string]int{
		"":                 100,
		"limit=25":         25,
		"limit=1000":       MaxListPageSize,
		"limit=1001":       MaxListPageSize,
		"limit=2000000000": MaxListPageSize,
		"limit=0":          100,
		"limit=-5":         100,
		"limit=abc":        100,
	}
	for query, want := range cases {
		got := h.ParseCommonFilter(httptest.NewRequestWithContext(t.Context(), "GET", "/api/workflows?"+query, nil)).Limit
		if got != want {
			t.Errorf("?%s gives a page of %d, want %d", query, got, want)
		}
	}
}
