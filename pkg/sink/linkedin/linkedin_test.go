package linkedin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod/pkg/message"
)

func TestLinkedInSink_Write(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ugcPosts" {
			t.Errorf("Expected path /ugcPosts, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id": "urn:li:share:123"}`))
	}))
	defer server.Close()

	sink := NewLinkedInSink("test-token", "urn:li:person:123", nil)
	sink.baseURL = server.URL

	msg := message.AcquireMessage()
	msg.SetPayload([]byte("Hello LinkedIn"))

	err := sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
}

func TestLinkedInSink_Ping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/me" {
			t.Errorf("Expected path /me, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewLinkedInSink("test-token", "urn:li:person:123", nil)
	sink.baseURL = server.URL

	err := sink.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
}
