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
