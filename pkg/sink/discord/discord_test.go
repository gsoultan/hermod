package discord

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod/pkg/message"
)

func TestDiscordSink_Write_Webhook(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewDiscordSink(server.URL, "", "", nil)
	msg := message.AcquireMessage()
	msg.SetPayload([]byte("test message"))

	err := sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDiscordSink_Write_Bot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bot test-token" {
			t.Errorf("expected Bot test-token, got %s", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewDiscordSink("", "test-token", "123", nil)
	sink.baseURL = server.URL
	msg := message.AcquireMessage()
	msg.SetPayload([]byte("test message"))

	err := sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
