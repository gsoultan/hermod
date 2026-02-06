package facebook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod/pkg/message"
)

func TestFacebookSink_Write(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-page/feed" {
			t.Errorf("Expected path /test-page/feed, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("access_token") != "test-token" {
			t.Errorf("Expected token test-token, got %s", r.URL.Query().Get("access_token"))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id": "post_123"}`))
	}))
	defer server.Close()

	sink := NewFacebookSink("test-token", "test-page", nil)
	sink.baseURL = server.URL

	msg := message.AcquireMessage()
	msg.SetPayload([]byte("Hello Facebook"))

	err := sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
}

func TestFacebookSink_Ping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-page" {
			t.Errorf("Expected path /test-page, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewFacebookSink("test-token", "test-page", nil)
	sink.baseURL = server.URL

	err := sink.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
}
