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
