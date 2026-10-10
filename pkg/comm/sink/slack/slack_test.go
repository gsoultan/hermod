package slack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

func TestSlackSink_Write_Webhook(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewSlackSink(server.URL, "", "", nil)
	msg := message.AcquireMessage()
	msg.SetPayload([]byte("test message"))

	err := sink.Write(t.Context(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSlackSink_Write_Bot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("expected Bearer test-token, got %s", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok": true}`))
	}))
	defer server.Close()

	sink := NewSlackSink("", "test-token", "C123", nil)
	sink.baseURL = server.URL
	msg := message.AcquireMessage()
	msg.SetPayload([]byte("test message"))

	err := sink.Write(t.Context(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A chat answered in Slack goes back to the channel it came from, and into the
// thread when it came from one: the sink's own channel is where its records go,
// not where a conversation happens to be.
func TestSlackSink_PostReply(t *testing.T) {
	var got struct {
		Channel  string `json:"channel"`
		ThreadTS string `json:"thread_ts"`
		Text     string `json:"text"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("request to %s with %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"ok": true}`))
	}))
	defer server.Close()

	sink := NewSlackSink("", "test-token", "C-sink", nil)
	sink.SetBaseURL(server.URL)
	if err := sink.PostReply(t.Context(), "C-chat", "1700000000.000100", "hello"); err != nil {
		t.Fatalf("PostReply: %v", err)
	}
	if got.Channel != "C-chat" || got.ThreadTS != "1700000000.000100" || got.Text != "hello" {
		t.Errorf("posted %+v", got)
	}
}

func TestSlackSink_PostReplyReportsAnAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok": false, "error": "not_in_channel"}`))
	}))
	defer server.Close()

	sink := NewSlackSink("", "test-token", "", nil)
	sink.SetBaseURL(server.URL)
	err := sink.PostReply(t.Context(), "C-chat", "", "hello")
	if err == nil || !strings.Contains(err.Error(), "not_in_channel") {
		t.Errorf("err = %v, want the API's error", err)
	}
}
