package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/user/hermod/internal/engine"
	"github.com/user/hermod/internal/sse"
	"github.com/user/hermod/internal/storage"
)

func TestSSESeparation(t *testing.T) {
	registry := engine.NewRegistry(nil)
	server := NewServer(registry, &mockStorage{}, nil, nil)
	handler := server.Routes()

	// 1. Verify new endpoint path
	req, _ := http.NewRequest("GET", "/streams/sse?stream=test", nil)
	rr := httptest.NewRecorder()

	// Use a context to stop the streaming handler
	ctx, cancel := context.WithTimeout(t.Context(), 1*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	// Start the handler in a goroutine
	done := make(chan bool)
	go func() {
		handler.ServeHTTP(rr, req)
		done <- true
	}()

	// Wait a bit for subscription to happen
	time.Sleep(100 * time.Millisecond)

	// 2. Publish to InternalHub - should NOT be received
	sse.GetInternalHub().Publish("test", sse.Event{
		ID:    "internal-1",
		Event: "test",
		Data:  []byte("internal data"),
	})

	// 3. Publish to DataHub - should BE received
	sse.GetDataHub().Publish("test", sse.Event{
		ID:    "data-1",
		Event: "test",
		Data:  []byte("data orchestration"),
	})

	// Wait for handler to finish or timeout
	select {
	case <-done:
	case <-ctx.Done():
	}

	body := rr.Body.String()

	// Verify data-1 is present
	if !strings.Contains(body, "id: data-1") {
		t.Errorf("Expected data-1 event in SSE stream, but not found. Body: %s", body)
	}
	if !strings.Contains(body, "data: data orchestration") {
		t.Errorf("Expected 'data orchestration' in SSE stream, but not found. Body: %s", body)
	}

	// Verify internal-1 is NOT present
	if strings.Contains(body, "id: internal-1") {
		t.Errorf("Found internal-1 event in data orchestration SSE stream, but it should be separated. Body: %s", body)
	}
}

func TestInternalSSERoute(t *testing.T) {
	registry := engine.NewRegistry(nil)
	server := NewServer(registry, &mockStorage{}, nil, nil)

	// 1. Verify internal endpoint path
	req, _ := http.NewRequest("GET", "/api/notifications/sse?stream=notifications", nil)

	// Add user context to bypass role check if any inside the handler
	user := &storage.User{Role: storage.RoleAdministrator}
	ctx := context.WithValue(t.Context(), userContextKey, user)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	// Use a context to stop the streaming handler
	ctx, cancel := context.WithTimeout(t.Context(), 1*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	// Start the handler directly to bypass authMiddleware
	done := make(chan bool)
	go func() {
		server.handleInternalSSE(rr, req)
		done <- true
	}()

	// Wait a bit for subscription to happen
	time.Sleep(100 * time.Millisecond)

	// 2. Publish to DataHub - should NOT be received
	sse.GetDataHub().Publish("notifications", sse.Event{
		ID:    "data-1",
		Event: "test",
		Data:  []byte("data orchestration"),
	})

	// 3. Publish to InternalHub - should BE received
	sse.GetInternalHub().Publish("notifications", sse.Event{
		ID:    "internal-1",
		Event: "test",
		Data:  []byte("internal data"),
	})

	// Wait for handler to finish or timeout
	select {
	case <-done:
	case <-ctx.Done():
	}

	body := rr.Body.String()

	// Verify internal-1 is present
	if !strings.Contains(body, "id: internal-1") {
		t.Errorf("Expected internal-1 event in internal SSE stream, but not found. Body: %s", body)
	}
	if !strings.Contains(body, "data: internal data") {
		t.Errorf("Expected 'internal data' in internal SSE stream, but not found. Body: %s", body)
	}

	// Verify data-1 is NOT present
	if strings.Contains(body, "id: data-1") {
		t.Errorf("Found data-1 event in internal SSE stream, but it should be separated. Body: %s", body)
	}
}
