package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/user/hermod/internal/engine"
	"github.com/user/hermod/internal/sse"
)

func TestSSESecurity(t *testing.T) {
	registry := engine.NewRegistry(nil)
	server := NewServer(registry, &mockStorage{}, nil, nil)
	handler := server.Routes()
	hub := sse.GetDataHub()

	t.Run("Unauthorized - No Token", func(t *testing.T) {
		hub.ConfigureStream("secure", sse.StreamConfig{AuthToken: "secret"})
		req, _ := http.NewRequest("GET", "/streams/sse?stream=secure", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", rr.Code)
		}
	})

	t.Run("Authorized - Header Token", func(t *testing.T) {
		hub.ConfigureStream("secure-header", sse.StreamConfig{AuthToken: "secret"})
		req, _ := http.NewRequest("GET", "/streams/sse?stream=secure-header", nil)
		req.Header.Set("Authorization", "Bearer secret")
		rr := httptest.NewRecorder()

		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		req = req.WithContext(ctx)

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rr.Code)
		}
	})

	t.Run("Authorized - Query Token", func(t *testing.T) {
		hub.ConfigureStream("secure-query", sse.StreamConfig{AuthToken: "secret"})
		req, _ := http.NewRequest("GET", "/streams/sse?stream=secure-query&token=secret", nil)
		rr := httptest.NewRecorder()

		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		req = req.WithContext(ctx)

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rr.Code)
		}
	})

	t.Run("Forbidden - Origin Not Allowed", func(t *testing.T) {
		hub.ConfigureStream("secure-origin", sse.StreamConfig{AllowedOrigins: []string{"https://trusted.com"}})
		req, _ := http.NewRequest("GET", "/streams/sse?stream=secure-origin", nil)
		req.Header.Set("Origin", "https://malicious.com")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("Expected status 403, got %d", rr.Code)
		}
	})

	t.Run("Allowed - Origin Matches", func(t *testing.T) {
		hub.ConfigureStream("secure-origin-ok", sse.StreamConfig{AllowedOrigins: []string{"https://trusted.com"}})
		req, _ := http.NewRequest("GET", "/streams/sse?stream=secure-origin-ok", nil)
		req.Header.Set("Origin", "https://trusted.com")
		rr := httptest.NewRecorder()

		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		req = req.WithContext(ctx)

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rr.Code)
		}
	})

	t.Run("Allowed - Wildcard Origin", func(t *testing.T) {
		hub.ConfigureStream("secure-wildcard", sse.StreamConfig{AllowedOrigins: []string{"*"}})
		req, _ := http.NewRequest("GET", "/streams/sse?stream=secure-wildcard", nil)
		req.Header.Set("Origin", "https://anywhere.com")
		rr := httptest.NewRecorder()

		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		req = req.WithContext(ctx)

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rr.Code)
		}
	})
}
