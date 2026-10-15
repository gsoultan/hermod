// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
