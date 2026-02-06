package twitter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod/pkg/message"
)

func TestTwitterSink_Write(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tweets" {
			t.Errorf("Expected path /tweets, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Expected Bearer test-token, got %s", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"data": {"id": "123", "text": "Hello"}}`))
	}))
	defer server.Close()

	sink := NewTwitterSink("test-token", nil)
	sink.baseURL = server.URL

	msg := message.AcquireMessage()
	msg.SetPayload([]byte("Hello World"))

	err := sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
}

func TestTwitterSink_Ping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/me" {
			t.Errorf("Expected path /users/me, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewTwitterSink("test-token", nil)
	sink.baseURL = server.URL

	err := sink.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
}
