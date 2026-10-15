// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
