package transformer

import (
	"context"
	"encoding/json"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPipelineTransformation(t *testing.T) {
	ctx := context.Background()

	// 1. Mock API server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Expecting user_id in URL
		if r.URL.Path == "/users/123" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": 123, "name": "External John", "role": "admin"}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	// 2. Define steps
	// Step 1: Filter - only process OpCreate
	step1 := &FilterOperationTransformer{
		Operations: map[hermod.Operation]bool{hermod.OpCreate: true},
	}

	// Step 2: HTTP call - fetch extra data
	step2 := &HttpTransformer{
		URL:    ts.URL + "/users/{user_id}",
		Method: "GET",
	}

	// Step 3: Mapping - map API result to final object
	step3 := &AdvancedTransformer{
		Mapping: map[string]string{
			"user_name": "source.name",
			"user_role": "source.role",
			"app":       "const.Hermod",
		},
	}

	pipeline := NewChain(step1, step2, step3)

	// 3. Test data
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetOperation(hermod.OpCreate)
	msg.SetAfter([]byte(`{"user_id": 123, "status": "active"}`))

	// 4. Run pipeline
	transformed, err := pipeline.Transform(ctx, msg)
	if err != nil {
		t.Fatalf("Pipeline failed: %v", err)
	}

	if transformed == nil {
		t.Fatal("Expected message, got nil")
	}

	var result map[string]interface{}
	if err := json.Unmarshal(transformed.After(), &result); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	if result["user_name"] != "External John" {
		t.Errorf("Expected user_name 'External John', got '%v'", result["user_name"])
	}
	if result["user_role"] != "admin" {
		t.Errorf("Expected user_role 'admin', got '%v'", result["user_role"])
	}
	if result["app"] != "Hermod" {
		t.Errorf("Expected app 'Hermod', got '%v'", result["app"])
	}
}
