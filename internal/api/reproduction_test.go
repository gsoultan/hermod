package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod/internal/engine"
	"github.com/user/hermod/internal/storage"
)

type mockReproductionStorage struct {
	fakeRBACStorage
}

func (m *mockReproductionStorage) CreateLog(ctx context.Context, log storage.Log) error {
	return nil
}

func TestTestTransformation_JSONUnmarshalError(t *testing.T) {
	reg := engine.NewRegistry(&mockReproductionStorage{})
	server := NewServer(reg, &mockReproductionStorage{}, nil, nil)

	// JSON payload with a number in transformation.config
	reqBody := map[string]interface{}{
		"transformation": map[string]interface{}{
			"type": "mapping",
			"config": map[string]interface{}{
				"key1": "value1",
				"key2": 123, // This was causing the error
			},
		},
		"message": map[string]interface{}{
			"foo": "bar",
		},
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/transformations/test", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	server.testTransformation(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	t.Logf("Response: %v", resp)
	// The mapping transformer might not do much with "key2": 123 if not configured,
	// but the point is that it should not fail to decode.
}
