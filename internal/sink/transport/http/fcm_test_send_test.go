package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
)

func postFcmTestSend(t *testing.T, body map[string]any) (int, map[string]string) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encoding request: %v", err)
	}
	h := &SinkHandler{Handler: &handlers.Handler{Registry: registry.NewRegistry(nil)}}
	rec := httptest.NewRecorder()
	h.SendFcmTestMessage(rec, httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/api/sinks/fcm/test-send", bytes.NewReader(encoded)))
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// A row the sink would refuse is answered with the setting to fix, before any
// credentials are used.
func TestFcmTestSendNamesTheSettingARowFailsOn(t *testing.T) {
	code, out := postFcmTestSend(t, map[string]any{
		"type": "fcm",
		"config": map[string]any{
			"credentials_json": `{"type":"service_account","project_id":"demo"}`,
			"device_token":     "{{.push_token}}",
			"title":            "Hi",
		},
		"sample": map[string]any{"id": "7"},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%v)", code, out)
	}
	if out["field"] != "device_token" || !strings.Contains(out["error"], "push_token") || out["fix"] == "" {
		t.Errorf("answer = %v, want the setting, the field it reads and the fix", out)
	}
}

func TestFcmTestSendRefusesOtherSinks(t *testing.T) {
	code, _ := postFcmTestSend(t, map[string]any{"type": "smtp", "config": map[string]any{}})
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}
