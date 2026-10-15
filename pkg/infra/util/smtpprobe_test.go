// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package util

import (
	"context"
	"testing"
	"time"
)

func TestVerifyEmailExists(t *testing.T) {
	tests := []struct {
		name    string
		email   string
		wantOK  bool
		wantErr bool
	}{
		{
			name:    "invalid format",
			email:   "invalid-email",
			wantOK:  false,
			wantErr: true,
		},
		{
			name:    "no @",
			email:   "user.domain.com",
			wantOK:  false,
			wantErr: true,
		},
		{
			name:    "no domain",
			email:   "user@",
			wantOK:  false,
			wantErr: true,
		},
		{
			name:    "valid format no MX",
			email:   "nobody@nonexistentmx.test",
			wantOK:  false,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()

			ok, reason := VerifyEmailExists(ctx, tt.email)
			if ok != tt.wantOK {
				t.Errorf("VerifyEmailExists() ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.wantErr && reason == "" {
				t.Errorf("VerifyEmailExists() reason empty on error")
			}
		})
	}
}
