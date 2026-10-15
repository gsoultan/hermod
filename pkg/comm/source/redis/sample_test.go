// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
