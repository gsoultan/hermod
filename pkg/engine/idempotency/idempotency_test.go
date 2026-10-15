// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package idempotency

import (
	"os"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

func TestDetermineIdempotencyKey_MetadataPreferred(t *testing.T) {
	m := message.AcquireMessage()
	defer message.ReleaseMessage(m)
	m.SetID("id-123")
	m.SetMetadata("idempotency_key", "meta-456")

	got := DetermineIdempotencyKey(m)
	if got != "meta-456" {
		t.Fatalf("expected metadata key, got %q", got)
	}
}

func TestEnsureIdempotencyID_SetsWhenEmpty(t *testing.T) {
	m := message.AcquireMessage()
	defer message.ReleaseMessage(m)
	m.SetMetadata("idempotency_key", "k-1")

	key, set := EnsureIdempotencyID(m)
	if key != "k-1" || !set {
		t.Fatalf("expected key 'k-1' to be set, got key=%q set=%v", key, set)
	}
	if m.ID() != "k-1" {
		t.Fatalf("expected message ID to be set to 'k-1', got %q", m.ID())
	}
}

func TestDetermineIdempotencyKey_Empty(t *testing.T) {
	m := message.AcquireMessage()
	defer message.ReleaseMessage(m)
	if got := DetermineIdempotencyKey(m); got != "" {
		t.Fatalf("expected empty key, got %q", got)
	}
}

func TestIdempotencyRequired(t *testing.T) {
	t.Setenv("HERMOD_IDEMPOTENCY_REQUIRED", "true")
	if !IdempotencyRequired() {
		t.Fatalf("expected required=true")
	}
	os.Unsetenv("HERMOD_IDEMPOTENCY_REQUIRED")
	if IdempotencyRequired() {
		t.Fatalf("expected required=false by default")
	}
}
