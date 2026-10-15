// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package redis

import (
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

func TestApplyStreamValues(t *testing.T) {
	tests := []struct {
		name       string
		values     map[string]any
		wantFields map[string]any
		wantAfter  string // when set, payload must be exposed via After()
	}{
		{
			name:       "DataKeyWithJSONExposesFields",
			values:     map[string]any{"data": `{"order_id":7,"status":"paid"}`},
			wantFields: map[string]any{"order_id": float64(7), "status": "paid"},
		},
		{
			name:      "DataKeyWithNonJSONFallsBackToAfter",
			values:    map[string]any{"data": "not-json"},
			wantAfter: "not-json",
		},
		{
			name:       "ArbitraryFieldsExposedWhenNoDataKey",
			values:     map[string]any{"temperature": "21.5", "unit": "C"},
			wantFields: map[string]any{"temperature": "21.5", "unit": "C"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := message.AcquireMessage()
			applyStreamValues(msg, tc.values)

			if tc.wantAfter != "" {
				if got := string(msg.After()); got != tc.wantAfter {
					t.Errorf("After() = %q; want %q", got, tc.wantAfter)
				}
				return
			}

			data := msg.Data()
			for k, want := range tc.wantFields {
				got, ok := data[k]
				if !ok {
					t.Errorf("missing field %q in stream data", k)
					continue
				}
				if got != want {
					t.Errorf("field %q = %v (%T); want %v (%T)", k, got, got, want, want)
				}
			}
		})
	}
}
