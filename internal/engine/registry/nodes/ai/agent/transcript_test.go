// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTranscript_IsBounded(t *testing.T) {
	var tr transcript
	long := strings.Repeat("é", maxEntryText)
	for i := range maxEntries + 10 {
		tr.add(entry{Step: i, Kind: "model", Text: long})
	}
	if len(tr.Entries) != maxEntries || !tr.Truncated {
		t.Fatalf("entries = %d truncated = %v", len(tr.Entries), tr.Truncated)
	}
	if tr.Entries[0].Step != 10 {
		t.Fatalf("the oldest entries must go first; first step = %d", tr.Entries[0].Step)
	}
	text := tr.Entries[0].Text
	if len(text) > maxEntryText+len("…[truncated]") || !utf8.ValidString(text) || !strings.HasSuffix(text, "[truncated]") {
		t.Fatalf("entry text was not clipped cleanly: %d bytes", len(text))
	}
}
