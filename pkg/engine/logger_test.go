package engine

import (
	"bytes"
	"errors"
	"testing"

	"github.com/rs/zerolog"
)

func TestDefaultLogger_ErrorHandling(t *testing.T) {
	var buf bytes.Buffer
	zl := zerolog.New(&buf)
	l := &DefaultLogger{logger: zl}

	err := errors.New("test error")
	l.Error("failed", "error", err)

	output := buf.String()
	if !contains(output, "test error") {
		t.Errorf("Expected output to contain 'test error', got: %s", output)
	}
	if !contains(output, "failed") {
		t.Errorf("Expected output to contain 'failed', got: %s", output)
	}
}

func contains(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}
