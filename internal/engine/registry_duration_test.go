package engine

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	testCases := []struct {
		input    string
		expected time.Duration
		err      bool
	}{
		{"1s", 1 * time.Second, false},
		{"1h", 1 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"1.5d", 36 * time.Hour, false},
		{" 1d ", 24 * time.Hour, false}, // Test trimming
		{"", 0, false},                  // Empty string
		{"invalid", 0, true},
		{"2w", 0, true}, // We only support up to days for now
	}

	for _, tc := range testCases {
		d, err := parseDuration(tc.input)
		if tc.err {
			if err == nil {
				t.Errorf("Expected error for input %q, but got none", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("Unexpected error for input %q: %v", tc.input, err)
			continue
		}
		if d != tc.expected {
			t.Errorf("Expected %v for input %q, but got %v", tc.expected, tc.input, d)
		}
	}
}
