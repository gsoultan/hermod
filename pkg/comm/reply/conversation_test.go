// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
