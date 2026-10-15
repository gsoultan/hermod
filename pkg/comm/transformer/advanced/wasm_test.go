// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package advanced

import (
	"encoding/base64"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/transformer"

	"github.com/gsoultan/hermod/pkg/comm/message"
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

	_, err := tr.Transform(t.Context(), msg, config)
	if err == nil {
		t.Fatal("expected error due to missing binary")
	}
}

func TestWasmTransformer_Registration(t *testing.T) {
	_, ok := transformer.Get("wasm")
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

	_, err := tr.Transform(t.Context(), msg, config)
	if err == nil {
		t.Fatal("expected error due to invalid wasm binary")
	}
}
