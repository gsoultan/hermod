package json

import (
	"testing"
)

func TestJSONFormatter_NilMessage(t *testing.T) {
	f := NewJSONFormatter()
	f.Mode = ModeFull
	_, err := f.Format(nil)
	if err == nil {
		t.Error("Expected error for nil message, got nil")
	}
}
