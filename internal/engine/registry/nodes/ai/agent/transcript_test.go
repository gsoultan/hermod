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
