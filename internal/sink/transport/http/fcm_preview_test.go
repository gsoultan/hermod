package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type fcmPreviewBody struct {
	Message       map[string]any `json:"message"`
	Recipients    int            `json:"recipients"`
	DataBytes     int            `json:"data_bytes"`
	SentDataBytes int            `json:"sent_data_bytes"`
	Limit         int            `json:"limit"`
	Largest       []struct {
		Key   string `json:"key"`
		Bytes int    `json:"bytes"`
	} `json:"largest"`
	Refused string         `json:"refused"`
	Sample  map[string]any `json:"sample"`
}

func postFcmPreview(t *testing.T, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encoding request: %v", err)
	}
	rec := httptest.NewRecorder()
	(&SinkHandler{}).PreviewFcmMessage(rec, httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/api/sinks/fcm/preview", bytes.NewReader(encoded)))
	return rec
}

func decodeFcmPreview(t *testing.T, rec *httptest.ResponseRecorder) fcmPreviewBody {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got fcmPreviewBody
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	return got
}

// From the stored shape of a sink — the flat string map the form writes — to
// the message FCM would be handed, through the factory the worker builds with.
func TestPreviewFcmMessage_RendersTheStoredConfigOverTheSampleRow(t *testing.T) {
	got := decodeFcmPreview(t, postFcmPreview(t, map[string]any{
		"type": "fcm",
		// No credentials_json: the form withholds it and a preview needs none.
		"config": map[string]string{
			"device_token": "{{.push_token}}",
			"title":        "Order {{.order_no}}",
			"body":         "{{.customer}}",
			"data_mode":    "none",
			"data_json":    `{"deeplink":"app://orders/{{.order_no}}"}`,
		},
		"sample": map[string]any{"order_no": "A-17", "customer": "Ana", "push_token": "tok-1"},
	}))

	if got.Refused != "" {
		t.Fatalf("refused: %s", got.Refused)
	}
	if got.Message["token"] != "tok-1" {
		t.Errorf("token = %v, want tok-1", got.Message["token"])
	}
	notification, _ := got.Message["notification"].(map[string]any)
	if notification["title"] != "Order A-17" || notification["body"] != "Ana" {
		t.Errorf("notification = %v", notification)
	}
	data, _ := got.Message["data"].(map[string]any)
	if len(data) != 1 || data["deeplink"] != "app://orders/A-17" {
		t.Errorf("data = %v, want only the listed value", data)
	}
	if got.Recipients != 1 || got.Limit != 4096 {
		t.Errorf("recipients = %d, limit = %d", got.Recipients, got.Limit)
	}
}

// The report that prompted this endpoint: the form's defaults send the whole
// row, a 4.6 KB row does not fit, and the run was the first thing to say so.
func TestPreviewFcmMessage_SaysARowIsTooBigBeforeARunDoes(t *testing.T) {
	got := decodeFcmPreview(t, postFcmPreview(t, map[string]any{
		"type":   "fcm",
		"config": map[string]string{"topic": "orders"},
		"sample": map[string]any{"id": "1", "notes": strings.Repeat("n", 4600)},
	}))

	if got.Refused == "" {
		t.Fatal("an oversized row previewed as deliverable")
	}
	if got.DataBytes <= got.Limit {
		t.Errorf("data bytes = %d against a limit of %d", got.DataBytes, got.Limit)
	}
	if got.Message != nil {
		t.Errorf("a refused message was returned as what would be sent: %v", got.Message)
	}
	if len(got.Largest) == 0 || got.Largest[0].Key != "payload" {
		t.Errorf("largest = %+v, want the payload named first", got.Largest)
	}
}

// With no sample the first press of the button still has to show something,
// and say what it was shown against.
func TestPreviewFcmMessage_UsesAnExampleRowWhenGivenNoSample(t *testing.T) {
	got := decodeFcmPreview(t, postFcmPreview(t, map[string]any{
		"type":   "fcm",
		"config": map[string]string{"topic": "orders", "title": "Order {{.id}}", "data_mode": "none"},
	}))
	if got.Refused != "" {
		t.Fatalf("refused: %s", got.Refused)
	}
	if got.Sample["id"] == nil {
		t.Errorf("the response does not say what it rendered against: %v", got.Sample)
	}
}

// Credentials are neither needed nor handed back. A request that does carry
// them — a client older than this form — must not get them echoed.
func TestPreviewFcmMessage_NeverEchoesCredentials(t *testing.T) {
	const secret = "-----BEGIN PRIVATE KEY-----preview-must-not-echo"
	rec := postFcmPreview(t, map[string]any{
		"type": "fcm",
		"config": map[string]string{
			"credentials_json": `{"type":"service_account","project_id":"p","private_key":"` + secret + `"}`,
			"topic":            "orders",
			"title":            "t",
			"data_mode":        "none",
		},
		"sample": map[string]any{"id": "1"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Error("the preview response contains the service account's private key")
	}
}

func TestPreviewFcmMessage_Refusals(t *testing.T) {
	cases := map[string]map[string]any{
		"another sink type": {
			"type":   "smtp",
			"config": map[string]string{"host": "smtp.example.com"},
		},
		"a template that does not parse": {
			"type":   "fcm",
			"config": map[string]string{"topic": "orders", "title": "{{.id"},
		},
		"a setting that does not parse": {
			"type":   "fcm",
			"config": map[string]string{"topic": "orders", "android_ttl": "soon"},
		},
		"an action that sends no message": {
			"type":   "fcm",
			"config": map[string]string{"action": "subscribe", "topic": "orders", "device_token": "{{.t}}"},
		},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := postFcmPreview(t, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}

	t.Run("a malformed body", func(t *testing.T) {
		rec := httptest.NewRecorder()
		(&SinkHandler{}).PreviewFcmMessage(rec, httptest.NewRequestWithContext(
			t.Context(), http.MethodPost, "/api/sinks/fcm/preview", strings.NewReader("{")))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status %d, want 400", rec.Code)
		}
	})
}

// A sink formatted through a schema registry reaches the registry to format a
// row. A preview that did the same would let whoever can edit a sink point the
// server at an address of their choosing, so it is refused before anything is
// built — and the stand-in registry here must never hear from it.
func TestPreviewFcmMessage_NeverContactsASchemaRegistry(t *testing.T) {
	var calls atomic.Int64
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(registry.Close)

	rec := postFcmPreview(t, map[string]any{
		"type": "fcm",
		"config": map[string]string{
			"topic":                   "orders",
			"format":                  "schema_registry",
			"schema_registry_url":     registry.URL,
			"schema_registry_subject": "orders-value",
			"schema_registry_schema":  `{"type":"record","name":"Order","fields":[{"name":"id","type":"string"}]}`,
		},
		"sample": map[string]any{"id": "1"},
	})

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("the preview made %d requests to the schema registry", n)
	}
}

// The sink form's config is not all text: `sequential` is a boolean from the
// moment the form mounts. A handler that decodes the config into a plain
// map[string]string answers every request from the real form with "cannot
// unmarshal bool" — found by pressing the button in a browser, after every
// test here had passed on hand-written, all-string configs.
func TestPreviewEndpointsAcceptTheConfigTheFormSends(t *testing.T) {
	formDefaults := map[string]any{"format": "json", "max_retries": "3", "retry_interval": "1s", "sequential": false}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range formDefaults {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	t.Run("fcm", func(t *testing.T) {
		got := decodeFcmPreview(t, postFcmPreview(t, map[string]any{
			"type":   "fcm",
			"config": with(map[string]any{"topic": "orders", "title": "Order {{.id}}"}),
			"sample": map[string]any{"id": "7"},
		}))
		if got.Refused != "" {
			t.Fatalf("refused: %s", got.Refused)
		}
	})

	t.Run("smtp", func(t *testing.T) {
		got := decodePreview(t, postPreview(t, map[string]any{
			"type": "smtp",
			"config": with(map[string]any{
				"from": "ops@example.com", "to": "ops@example.com", "subject": "Order {{.id}}", "template": "hi",
			}),
			"sample": map[string]any{"id": "7"},
		}))
		if got.Subject != "Order 7" {
			t.Errorf("subject = %q, want Order 7", got.Subject)
		}
	})
}
