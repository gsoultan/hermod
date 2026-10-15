// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package idempotency

import (
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

const idempotencyKeyMeta = "idempotency_key"

// DetermineIdempotencyKey returns a deterministic key for deduplication.
// Precedence:
// 1) metadata[idempotency_key]
// 2) message ID
// 3) empty string if none available (caller may decide behavior)
func DetermineIdempotencyKey(msg hermod.Message) string {
	if msg == nil {
		return ""
	}
	if v, ok := hermod.MetadataValue(msg, idempotencyKeyMeta); ok {
		if k := strings.TrimSpace(v); k != "" {
			return k
		}
	}
	if id := strings.TrimSpace(msg.ID()); id != "" {
		return id
	}
	return ""
}

// EnsureIdempotencyID ensures the message has a stable ID set for sinks that
// use the message ID as the natural idempotency key (e.g., SQL primary key, etc.).
// If no ID or idempotency key is present, it generates a UUID as a defensive
// backstop to ensure the message is traceable and uniquely identifiable.
// Returns the key and whether it was set on the message.
func EnsureIdempotencyID(msg hermod.Message) (string, bool) {
	key := DetermineIdempotencyKey(msg)
	if key == "" {
		key = uuid.NewString()
		if dm, ok := msg.(*message.DefaultMessage); ok {
			dm.SetID(key)
			return key, true
		}
		return key, false
	}
	if dm, ok := msg.(*message.DefaultMessage); ok {
		if strings.TrimSpace(dm.ID()) == "" {
			dm.SetID(key)
			return key, true
		}
	}
	return key, false
}

// IdempotencyRequired returns true if idempotency must be enforced (and missing
// keys should be reported). Controlled via HERMOD_IDEMPOTENCY_REQUIRED.
func IdempotencyRequired() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("HERMOD_IDEMPOTENCY_REQUIRED")))
	return v == "1" || v == "true" || v == "yes"
}
