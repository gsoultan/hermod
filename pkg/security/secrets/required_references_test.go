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

// A reference with a fallback answers something when the secret is missing; a
// reference without one answers nothing. Only the second kind can be said to
// need the secret.
func TestRequiredReferencesAreTheOnesWithoutAFallback(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  []Ref
	}{
		{`secret("API_KEY")`, []Ref{{"secret", "API_KEY"}}},
		{`secret("API_KEY", "none")`, nil},
		{`secret( 'API_KEY' , 'none' )`, nil},
		{`secret.API_KEY`, nil}, // bare and no call: prose, as References reads it
		{`concat("Bearer ", secret.API_KEY)`, []Ref{{"secret", "API_KEY"}}},
		{`concat(secret("A", "x"), env("B"))`, []Ref{{"env", "B"}}},
		{`{{secret.API_KEY}}`, []Ref{{"secret", "API_KEY"}}},
		{`{{secret("API_KEY", "none")}}`, nil},
		{`{{upper(secret("API_KEY"))}}`, []Ref{{"secret", "API_KEY"}}},
		{`Bearer {{env.TOKEN}} for {{secret("K","d")}}`, []Ref{{"env", "TOKEN"}}},
		// Read once with a fallback and once without: the one without needs it.
		{`concat(secret("A", "x"), secret("A"))`, []Ref{{"secret", "A"}}},
		{`source.secret.token`, nil},
		{`plain text`, nil},
	} {
		if got := RequiredReferences(tc.value); !slices.Equal(got, tc.want) {
			t.Errorf("RequiredReferences(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}
