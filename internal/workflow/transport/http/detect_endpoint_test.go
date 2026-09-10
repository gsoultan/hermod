package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer/security"
)

func postDetect(t *testing.T, sample, key string) (int, security.DetectionResult, string) {
	t.Helper()

	body, err := json.Marshal(map[string]any{"sample": sample, "key": key})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	h := &WorkflowHandler{Handler: &handlers.Handler{}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/transformations/detect-decryption", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.DetectDecryptionSettings(rec, req)

	var out security.DetectionResult
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out, rec.Body.String()
}

// TestDetectEndpointFindsTheSettings drives the endpoint the editor's Detect
// button calls, against a value framed the way an external system frames one.
func TestDetectEndpointFindsTheSettings(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef"
	sample := sealRawSample(t, key)

	code, res, raw := postDetect(t, sample, key)
	if code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", code, raw)
	}
	if len(res.Candidates) == 0 {
		t.Fatalf("no candidates; reason=%s", res.Reason)
	}
	best := res.Candidates[0]
	if best.Confidence != security.ConfidenceCertain {
		t.Errorf("confidence: got %q", best.Confidence)
	}
	if best.Config["aadMode"] != "key" || best.Config["format"] != "raw" {
		t.Errorf("config did not describe the sample: %+v", best.Config)
	}
}

// TestDetectEndpointNeverEchoesTheKey: the key is a secret the caller sent, and
// a response carrying it back widens where it can be logged or cached.
func TestDetectEndpointNeverEchoesTheKey(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef"
	sample := sealRawSample(t, key)

	_, _, raw := postDetect(t, sample, key)
	if strings.Contains(raw, key) {
		t.Fatalf("the response echoed the key: %s", raw)
	}
}

// TestDetectEndpointIsNotCached: the response contains a plaintext preview, so
// a proxy or browser copy is a copy nobody asked for.
func TestDetectEndpointIsNotCached(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"sample": "x", "key": "y"})
	h := &WorkflowHandler{Handler: &handlers.Handler{}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/transformations/detect-decryption", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.DetectDecryptionSettings(rec, req)

	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control: got %q, want no-store", got)
	}
}

// TestDetectEndpointExplainsFailure: an empty candidate list must carry a
// reason, or the button looks broken rather than the input being wrong.
func TestDetectEndpointExplainsFailure(t *testing.T) {
	code, res, raw := postDetect(t, "bm90IGNpcGhlcnRleHQgYXQgYWxs", "the wrong key")
	if code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", code, raw)
	}
	if len(res.Candidates) == 0 && res.Reason == "" {
		t.Error("an empty result must explain itself")
	}
}

// TestDetectEndpointRejectsBadJSON keeps a malformed body a 400 rather than a
// panic or a silent empty answer.
func TestDetectEndpointRejectsBadJSON(t *testing.T) {
	h := &WorkflowHandler{Handler: &handlers.Handler{}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/transformations/detect-decryption", strings.NewReader("{not json"))
	rec := httptest.NewRecorder()
	h.DetectDecryptionSettings(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

// sealRawSample builds a raw-format sample with the key doubling as the AAD,
// which is the shape that motivated detection.
func sealRawSample(t *testing.T, key string) string {
	t.Helper()

	msg := newDetectMsg(t)
	res, err := (&security.EncryptTransformer{}).Transform(t.Context(), msg, map[string]any{
		"field": "v", "key": key, "keyFormat": "raw",
		"algorithm": "aes-256-gcm", "format": "raw", "encoding": "base64",
		"aadMode": "key",
	})
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	s, _ := res.Data()["v"].(string)
	return s
}

func newDetectMsg(t *testing.T) *message.DefaultMessage {
	t.Helper()
	m := message.AcquireMessage()
	t.Cleanup(func() { message.ReleaseMessage(m) })
	m.SetID("1")
	m.SetData("v", `{"user_id":"01a0411f","email":"someone@example.com"}`)
	return m
}
