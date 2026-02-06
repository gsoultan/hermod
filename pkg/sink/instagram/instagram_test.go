package instagram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod/pkg/message"
)

func TestInstagramSink_Write(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id": "12345"}`))
	}))
	defer server.Close()

	sink := NewInstagramSink("test-token", "test-ig-user", nil)
	sink.baseURL = server.URL

	msg := message.AcquireMessage()
	msg.SetPayload([]byte("Hello Instagram"))
	msg.SetData("media_url", "https://example.com/image.jpg")

	err := sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
}

func TestInstagramSink_Ping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-ig-user" {
			t.Errorf("Expected path /test-ig-user, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewInstagramSink("test-token", "test-ig-user", nil)
	sink.baseURL = server.URL

	err := sink.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
}
