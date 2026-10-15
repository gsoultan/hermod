// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
