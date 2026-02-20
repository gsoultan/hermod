package transformer

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/user/hermod/pkg/message"
)

func TestWasmTransformer_Transform_MissingBinary(t *testing.T) {
	tr := &WasmTransformer{}
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetID("1")
	msg.SetData("foo", "bar")
	config := map[string]any{
		"function": "transform",
	}

	_, err := tr.Transform(context.Background(), msg, config)
	if err == nil {
		t.Fatal("expected error due to missing binary")
	}
}

func TestWasmTransformer_Registration(t *testing.T) {
	_, ok := Get("wasm")
	if !ok {
		t.Fatal("wasm transformer not registered")
	}
}

// Note: A full functional test would require a valid WASM binary that follows the
// Hermod WASI pattern (JSON from stdin, JSON to stdout).
func TestWasmTransformer_Base64Invalid(t *testing.T) {
	tr := &WasmTransformer{}
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetID("1")
	msg.SetData("foo", "bar")

	// Invalid WASM binary
	config := map[string]any{
		"wasmBytes": base64.StdEncoding.EncodeToString([]byte("invalid wasm")),
	}

	_, err := tr.Transform(context.Background(), msg, config)
	if err == nil {
		t.Fatal("expected error due to invalid wasm binary")
	}
}
