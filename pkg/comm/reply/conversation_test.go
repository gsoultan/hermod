// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
