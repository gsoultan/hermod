package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/user/hermod/internal/config"
)

func TestSaveDBConfigWithMasterKey(t *testing.T) {
	// Setup: Remove db_config.yaml if exists
	os.Remove(config.DBConfigPath)
	defer os.Remove(config.DBConfigPath)

	server := NewServer(nil, nil, nil, nil)

	tests := []struct {
		name           string
		payload        map[string]string
		expectedStatus int
	}{
		{
			name: "Valid config with master key",
			payload: map[string]string{
				"type":              "sqlite",
				"conn":              "test_setup.db",
				"crypto_master_key": "this-is-a-valid-32-byte-long-key-!!",
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "Invalid config - short master key",
			payload: map[string]string{
				"type":              "sqlite",
				"conn":              "test_setup.db",
				"crypto_master_key": "too-short",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid config - missing master key",
			payload: map[string]string{
				"type": "sqlite",
				"conn": "test_setup.db",
			},
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.payload)
			req := httptest.NewRequest("POST", "/api/config/database", bytes.NewReader(body))
			rr := httptest.NewRecorder()

			server.saveDBConfig(rr, req)

			if rr.Code != tt.expectedStatus {
				t.Errorf("expected status %v, got %v. Body: %s", tt.expectedStatus, rr.Code, rr.Body.String())
			}

			if tt.expectedStatus == http.StatusOK {
				// Verify it was saved
				cfg, err := config.LoadDBConfig()
				if err != nil {
					t.Fatalf("failed to load saved config: %v", err)
				}
				if cfg.CryptoMasterKey != tt.payload["crypto_master_key"] {
					t.Errorf("expected master key %v, got %v", tt.payload["crypto_master_key"], cfg.CryptoMasterKey)
				}
			}
		})
	}
}
