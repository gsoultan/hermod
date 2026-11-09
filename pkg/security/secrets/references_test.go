// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package secrets

import (
	"slices"
	"testing"
)

func TestReferencesFindsEverySpellingTheEngineResolves(t *testing.T) {
	cases := []struct {
		value string
		want  []Ref
	}{
		{"{{secret.A}}", []Ref{{"secret", "A"}}},
		{"{{ secret.A }}", []Ref{{"secret", "A"}}},
		{"{{SECRET.A}}", []Ref{{"secret", "A"}}},
		{`secret("B")`, []Ref{{"secret", "B"}}},
		{`{{secret("F")}}`, []Ref{{"secret", "F"}}},
		{`{{ secret('G', "x") }}`, []Ref{{"secret", "G"}}},
		{"{{upper(secret.C)}}", []Ref{{"secret", "C"}}},
		{`{{concat("Bearer ", secret.TOKEN)}}`, []Ref{{"secret", "TOKEN"}}},
		{`concat(secret.D, "x")`, []Ref{{"secret", "D"}}},
		{"secret:E", []Ref{{"secret", "E"}}},
		{"{{secret:E}}", []Ref{{"secret", "E"}}},
		{"Bearer {{secret.J}} and {{secret.K}}", []Ref{{"secret", "J"}, {"secret", "K"}}},
		{"{{ env.REGION }}", []Ref{{"env", "REGION"}}},
		{`lower(env("ZONE"))`, []Ref{{"env", "ZONE"}}},
		{"{{secret.A}} {{secret.A}}", []Ref{{"secret", "A"}}},
		// Not references.
		{"{{.secret.x}}", nil},
		{"{{.after.id}}", nil},
		{"{{upper(source.secret.x)}}", nil},
		{"{{secret.A.b}}", nil},
		{"my secret.txt", nil},
		{"read secret.txt (old)", nil},
		{"env:prod", nil},
		{"secret:", nil},
		{"plain", nil},
		{"", nil},
	}
	for _, c := range cases {
		if got := References(c.value); !slices.Equal(got, c.want) {
			t.Errorf("References(%q) = %v, want %v", c.value, got, c.want)
		}
	}
}
