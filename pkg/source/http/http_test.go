package sourcehttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPSource_Read(t *testing.T) {
	// Mock server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/array" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": 1, "name": "item1"},
				{"id": 2, "name": "item2"},
			})
		} else if r.URL.Path == "/nested" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data": []map[string]any{
					{"id": 3, "name": "item3"},
					{"id": 4, "name": "item4"},
				},
			})
		} else {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"id": 0, "name": "single"})
		}
	}))
	defer ts.Close()

	t.Run("Single Object", func(t *testing.T) {
		source := NewHTTPSource(ts.URL, "GET", nil, 100*time.Millisecond, "")
		msg, err := source.Read(context.Background())
		if err != nil {
			t.Fatalf("Read failed: %v", err)
		}
		if msg.Data()["id"].(float64) != 0 {
			t.Errorf("Expected id 0, got %v", msg.Data()["id"])
		}
	})

	t.Run("Array Response", func(t *testing.T) {
		source := NewHTTPSource(ts.URL+"/array", "GET", nil, 100*time.Millisecond, "")

		// First read
		msg1, err := source.Read(context.Background())
		if err != nil {
			t.Fatalf("First read failed: %v", err)
		}
		if msg1.Data()["id"].(float64) != 1 {
			t.Errorf("Expected id 1, got %v", msg1.Data()["id"])
		}

		// Second read (should come from buffer)
		msg2, err := source.Read(context.Background())
		if err != nil {
			t.Fatalf("Second read failed: %v", err)
		}
		if msg2.Data()["id"].(float64) != 2 {
			t.Errorf("Expected id 2, got %v", msg2.Data()["id"])
		}
	})

	t.Run("Nested Array with GJSON", func(t *testing.T) {
		source := NewHTTPSource(ts.URL+"/nested", "GET", nil, 100*time.Millisecond, "data")

		msg1, err := source.Read(context.Background())
		if err != nil {
			t.Fatalf("First read failed: %v", err)
		}
		if msg1.Data()["id"].(float64) != 3 {
			t.Errorf("Expected id 3, got %v", msg1.Data()["id"])
		}

		msg2, err := source.Read(context.Background())
		if err != nil {
			t.Fatalf("Second read failed: %v", err)
		}
		if msg2.Data()["id"].(float64) != 4 {
			t.Errorf("Expected id 4, got %v", msg2.Data()["id"])
		}
	})
}
