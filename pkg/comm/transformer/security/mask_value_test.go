// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
