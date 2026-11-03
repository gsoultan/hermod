// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package storage

import (
	"slices"
	"testing"
)

func TestRenameInVHostList(t *testing.T) {
	cases := []struct {
		name    string
		in      []string
		want    []string
		changed bool
	}{
		{"renames in place", []string{"ops", "sales", "eu"}, []string{"ops", "revenue", "eu"}, true},
		{"not listed", []string{"ops"}, []string{"ops"}, false},
		{"empty", nil, nil, false},
		{"already has the new name", []string{"revenue", "sales"}, []string{"revenue"}, true},
		{"listed twice", []string{"sales", "sales"}, []string{"revenue"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := slices.Clone(tc.in)
			got, changed := RenameInVHostList(in, "sales", "revenue")
			if changed != tc.changed || !slices.Equal(got, tc.want) {
				t.Errorf("RenameInVHostList(%v) = %v, %v; want %v, %v", tc.in, got, changed, tc.want, tc.changed)
			}
			if !slices.Equal(in, tc.in) {
				t.Errorf("the input was modified: %v", in)
			}
		})
	}
}
