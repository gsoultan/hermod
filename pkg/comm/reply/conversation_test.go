// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package reply

import (
	"strings"
	"testing"
)

func TestValidConversationID(t *testing.T) {
	for _, id := range []string{"c-1", "user:42.thread_7", strings.Repeat("a", 128)} {
		if !ValidConversationID(id) {
			t.Errorf("%q rejected", id)
		}
	}
	for _, id := range []string{"", "has space", "slash/x", "new\nline", strings.Repeat("a", 129), "ünï"} {
		if ValidConversationID(id) {
			t.Errorf("%q accepted", id)
		}
	}
}

func TestNewConversationIDIsValidAndUnique(t *testing.T) {
	a, b := NewConversationID(), NewConversationID()
	if !ValidConversationID(a) || a == b {
		t.Fatalf("ids %q %q", a, b)
	}
}
