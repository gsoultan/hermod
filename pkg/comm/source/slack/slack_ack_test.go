package slack

// The cursor said a page was consumed the moment it was fetched.
//
// Read pulls up to 50 messages, sets lastTimestamp to the newest of them, and
// then hands them out one at a time. GetState persists lastTimestamp and Ack
// did nothing at all. So a crash after delivering the first item of a page
// lost the rest: the stored cursor had already moved past them, and nothing
// will ever fetch that window again. At-most-once, silently — in a connector
// documented as at-least-once.
//
// The fix separates the two cursors that were being conflated: the one Read
// uses to fetch the next page, which must run ahead, and the one GetState
// persists, which may only move behind acknowledged messages.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// slackServer serves one page of messages, newest first, the way Slack does.
func slackServer(t *testing.T, tss ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		msgs := make([]map[string]any, 0, len(tss))
		// Slack returns newest first; the source reverses them.
		for i := len(tss) - 1; i >= 0; i-- {
			msgs = append(msgs, map[string]any{"ts": tss[i], "text": "m" + tss[i], "user": "U1"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": msgs})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestSource(t *testing.T, srv *httptest.Server) *SlackSource {
	t.Helper()
	s := NewSlackSource("token", "C1", time.Hour) // one poll is all these tests need
	s.SetBaseURL(srv.URL)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStateDoesNotAdvancePastUnacknowledgedMessages(t *testing.T) {
	srv := slackServer(t, "100", "200", "300")
	s := newTestSource(t, srv)

	// Read the whole page. Nothing is acknowledged.
	for range 3 {
		if _, err := s.Read(context.Background()); err != nil {
			t.Fatalf("Read: %v", err)
		}
	}

	if got := s.GetState()["last_timestamp"]; got != "" {
		t.Errorf("state says %q was consumed, but nothing was acknowledged; "+
			"a restart here loses every message in the page", got)
	}
}

func TestStateAdvancesOnlyBehindAcknowledgedMessages(t *testing.T) {
	srv := slackServer(t, "100", "200", "300")
	s := newTestSource(t, srv)
	ctx := context.Background()

	msgs := make([]string, 0, 3)
	for range 3 {
		m, err := s.Read(ctx)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		msgs = append(msgs, m.ID())
	}
	if msgs[0] != "100" || msgs[2] != "300" {
		t.Fatalf("precondition: messages came out as %v, want oldest first", msgs)
	}

	// Out of order on purpose: the last one first. It may not move the cursor,
	// because the two before it are still in flight.
	if err := s.Ack(ctx, stubMsg(msgs[2])); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if got := s.GetState()["last_timestamp"]; got != "" {
		t.Errorf("state = %q after acknowledging only the newest; the two before it are still in flight", got)
	}

	_ = s.Ack(ctx, stubMsg(msgs[0]))
	if got := s.GetState()["last_timestamp"]; got != "100" {
		t.Errorf("state = %q after acknowledging the oldest, want 100", got)
	}

	_ = s.Ack(ctx, stubMsg(msgs[1]))
	if got := s.GetState()["last_timestamp"]; got != "300" {
		t.Errorf("state = %q once the run was complete, want 300 (the prefix closes over all three)", got)
	}
}

// Resuming must pick up from the acknowledged cursor, and re-fetch from there.
func TestSetStateResumesFromTheAcknowledgedCursor(t *testing.T) {
	srv := slackServer(t, "400")
	s := newTestSource(t, srv)

	s.SetState(map[string]string{"last_timestamp": "300"})
	if got := s.GetState()["last_timestamp"]; got != "300" {
		t.Fatalf("state = %q after SetState, want 300", got)
	}

	if _, err := s.Read(context.Background()); err != nil {
		t.Fatalf("Read: %v", err)
	}
	// Still 300: the newly read message has not been acknowledged.
	if got := s.GetState()["last_timestamp"]; got != "300" {
		t.Errorf("state = %q after reading an unacknowledged message, want 300", got)
	}
}

// stubMsg is just an identity: Ack only needs the message's ID.
type stubMessage struct {
	hermod.Message
	id string
}

func (m stubMessage) ID() string { return m.id }

func stubMsg(id string) hermod.Message { return stubMessage{id: id} }
