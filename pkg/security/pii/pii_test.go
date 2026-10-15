// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package pii

import (
	"slices"
	"testing"
)

func TestEngine_Mask(t *testing.T) {
	engine := NewEngine()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Credit Card",
			input:    "My card is 4111-1111-1111-1111",
			expected: "My card is ****-****-****-****",
		},
		{
			name:     "SSN",
			input:    "SSN: 123-45-6789",
			expected: "SSN: ***-**-****",
		},
		{
			name:     "Email",
			input:    "Contact me at john.doe@example.com",
			expected: "Contact me at ****@****.***",
		},
		{
			name:     "Combined",
			input:    "User john.doe@example.com has IP 192.168.1.1",
			expected: "User ****@****.*** has IP *.*.*.*",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := engine.Mask(tt.input)
			if got != tt.expected {
				t.Errorf("Mask() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestEngine_Discover(t *testing.T) {
	engine := NewEngine()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "Credit Card",
			input:    "4111-1111-1111-1111",
			expected: []string{"Credit Card"},
		},
		{
			name:     "Multiple",
			input:    "john.doe@example.com is at 1.1.1.1",
			expected: []string{"IPv4", "Email"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := engine.Discover(tt.input)
			if len(got) != len(tt.expected) {
				t.Errorf("Discover() returned %d items, want %d", len(got), len(tt.expected))
			}
			// Simplified check
			for _, exp := range tt.expected {
				found := slices.Contains(got, exp)
				if !found {
					t.Errorf("Discover() did not find %s", exp)
				}
			}
		})
	}
}
