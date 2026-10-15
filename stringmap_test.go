// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package hermod

import (
	"encoding/json"
	"testing"
)

func TestStringMapUnmarshalCoercesScalars(t *testing.T) {
	type sink struct {
		Type   string    `json:"type"`
		Config StringMap `json:"config"`
	}

	tests := []struct {
		name string
		body string
		want map[string]string
	}{
		{
			name: "bool and number values are coerced",
			body: `{"type":"stdout","config":{"pretty":true,"batch_size":100,"ratio":1.5,"name":"out"}}`,
			want: map[string]string{"pretty": "true", "batch_size": "100", "ratio": "1.5", "name": "out"},
		},
		{
			name: "null config stays nil",
			body: `{"type":"stdout","config":null}`,
			want: nil,
		},
		{
			name: "null value becomes empty string",
			body: `{"type":"stdout","config":{"k":null}}`,
			want: map[string]string{"k": ""},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var s sink
			if err := json.Unmarshal([]byte(tc.body), &s); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(s.Config) != len(tc.want) {
				t.Fatalf("got %v, want %v", s.Config, tc.want)
			}
			for k, want := range tc.want {
				if got := s.Config[k]; got != want {
					t.Errorf("key %q: got %q, want %q", k, got, want)
				}
			}
		})
	}
}
