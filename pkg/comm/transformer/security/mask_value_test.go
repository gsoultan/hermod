// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
