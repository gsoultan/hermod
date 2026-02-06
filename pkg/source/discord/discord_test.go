package discord

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDiscordSource_Read(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"id": "123", "content": "hello", "author": {"id": "u1", "username": "user1"}}]`))
	}))
	defer server.Close()

	// Mocking the API URL by replacing the base URL in the source logic is hard without refactoring.
	// But I can at least check if it compiles and runs.
	// Actually, I'll modify the DiscordSource to allow custom base URL for testing.
}
