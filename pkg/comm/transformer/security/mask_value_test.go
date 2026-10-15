// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package security

import "testing"

// MaskValue is the mask node's own masking, for callers outside a workflow
// node (the Collect Dataset sink): the same value in, the same mask out.
func TestMaskValueMasksAsTheMaskNodeDoes(t *testing.T) {
	tests := []struct {
		in, maskType, want string
	}{
		{"ada@example.com", "email", "a****@example.com"},
		{"4111111111111111", "partial", "41****11"},
		{"abc", "partial", "****"},
		{"secret", "all", "****"},
		{"secret", "", "****"},
	}
	for _, tt := range tests {
		t.Run(tt.maskType+"/"+tt.in, func(t *testing.T) {
			if got := MaskValue(tt.in, tt.maskType); got != tt.want {
				t.Errorf("MaskValue(%q, %q) = %q, want %q", tt.in, tt.maskType, got, tt.want)
			}
		})
	}
}
