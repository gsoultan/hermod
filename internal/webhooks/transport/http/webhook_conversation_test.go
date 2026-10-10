package http

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/reply"
)

var syncMode = map[string]string{reply.ConfigMode: reply.ModeSync, reply.ConfigTimeout: "5s"}

func conversationOf(t *testing.T, r syncReply) string {
	t.Helper()
	var rec struct {
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(r.Record, &rec); err != nil {
		t.Fatalf("record: %v: %s", err, r.Record)
	}
	return rec.Metadata[reply.MetaConversationID]
}

// A chat caller names its conversation; the workflow sees it on the message
// and the answer echoes it.
func TestASynchronousWebhookCarriesTheConversationID(t *testing.T) {
	rec := postWith(t, "chat", syncMode, &recordingSink{}, map[string]string{reply.HeaderConversationID: "conv-42"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get(reply.HeaderConversationID); got != "conv-42" {
		t.Errorf("header echoed %q", got)
	}
	var body struct {
		ConversationID string `json:"conversation_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.ConversationID != "conv-42" {
		t.Errorf("body conversation_id = %q", body.ConversationID)
	}
	if got := conversationOf(t, decode(t, rec)); got != "conv-42" {
		t.Errorf("the message carried conversation %q", got)
	}
}

// A synchronous caller that names no conversation starts one, and is told its
// id so it can continue it.
func TestASynchronousWebhookStartsAConversation(t *testing.T) {
	rec := postWith(t, "chat", syncMode, &recordingSink{}, nil)
	id := rec.Header().Get(reply.HeaderConversationID)
	if !reply.ValidConversationID(id) {
		t.Fatalf("no conversation id was issued: %q", id)
	}
	if got := conversationOf(t, decode(t, rec)); got != id {
		t.Errorf("the message carried %q, the caller was given %q", got, id)
	}
}

func TestAnInvalidConversationIDIsRefused(t *testing.T) {
	sink := &recordingSink{}
	rec := postWith(t, "chat", syncMode, sink, map[string]string{reply.HeaderConversationID: "../../etc"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if sink.count() != 0 {
		t.Error("a refused request was dispatched")
	}
}

// An asynchronous webhook passes a conversation id through but does not make
// one up: nobody is there to receive it.
func TestAnAsynchronousWebhookDoesNotIssueAConversation(t *testing.T) {
	rec := postWith(t, "async", nil, &recordingSink{}, nil)
	if got := rec.Header().Get(reply.HeaderConversationID); got != "" {
		t.Errorf("issued %q", got)
	}
}
