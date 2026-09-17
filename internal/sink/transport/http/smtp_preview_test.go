package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type previewResponse struct {
	Subject string         `json:"subject"`
	From    string         `json:"from"`
	To      []string       `json:"to"`
	Body    string         `json:"body"`
	HTML    bool           `json:"html"`
	Sample  map[string]any `json:"sample"`
}

func postPreview(t *testing.T, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encoding request: %v", err)
	}
	rec := httptest.NewRecorder()
	(&SinkHandler{}).PreviewSmtpTemplate(rec, httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/api/sinks/smtp/preview", bytes.NewReader(encoded)))
	return rec
}

func decodePreview(t *testing.T, rec *httptest.ResponseRecorder) previewResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got previewResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	return got
}

func TestPreviewSmtpTemplate_RendersOverTheSampleRow(t *testing.T) {
	got := decodePreview(t, postPreview(t, map[string]any{
		"type": "smtp",
		"config": map[string]string{
			"host":     "smtp.example.com",
			"from":     "ops@example.com",
			"to":       "{{ .email }}",
			"subject":  `Order {{ .id }} on {{ .created_at.Format "02 Jan 2006" }}`,
			"template": "<p>Hi {{ .name }}</p>",
		},
		"sample": map[string]any{
			"id":         "42",
			"email":      "buyer@example.com",
			"name":       "Ana",
			"created_at": "2026-12-01T09:30:00Z",
		},
	}))

	if want := "Order 42 on 01 Dec 2026"; got.Subject != want {
		t.Errorf("subject = %q, want %q", got.Subject, want)
	}
	if len(got.To) != 1 || got.To[0] != "buyer@example.com" {
		t.Errorf("recipients = %v, want [buyer@example.com]", got.To)
	}
	if want := "<p>Hi Ana</p>"; got.Body != want {
		t.Errorf("body = %q, want %q", got.Body, want)
	}
	if !got.HTML {
		t.Error("an HTML body was reported as plaintext")
	}
}

// With no sample the preview still has to render something, or the first thing
// an operator does — write a template and press the button — shows nothing.
func TestPreviewSmtpTemplate_RendersAgainstAnExampleRowWhenGivenNoSample(t *testing.T) {
	got := decodePreview(t, postPreview(t, map[string]any{
		"type": "smtp",
		"config": map[string]string{
			"from":     "ops@example.com",
			"to":       "ops@example.com",
			"subject":  "{{ .table }}",
			"template": `{{ .created_at.Format "2006-01-02" }}`,
		},
	}))
	if got.Subject == "" || strings.Contains(got.Subject, "<no value>") {
		t.Errorf("subject = %q, want the example row's table", got.Subject)
	}
	if got.Body == "" || strings.Contains(got.Body, "<no value>") {
		t.Errorf("body = %q, want a formatted example date", got.Body)
	}
	if got.Sample["created_at"] == nil {
		t.Errorf("the response does not say what it rendered against: %v", got.Sample)
	}
}

func TestPreviewSmtpTemplate_ReportsABrokenTemplate(t *testing.T) {
	rec := postPreview(t, map[string]any{
		"type": "smtp",
		"config": map[string]string{
			"from": "ops@example.com", "to": "ops@example.com", "subject": "s",
			"template": "{{ .name ",
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "template") {
		t.Errorf("error %q does not point at the template", rec.Body.String())
	}
}

// A preview must not make the server fetch a URL or an S3 object for whoever
// can edit a sink: the response would hand back whatever came out.
func TestPreviewSmtpTemplate_RefusesARemoteTemplate(t *testing.T) {
	for _, source := range []string{"url", "s3"} {
		t.Run(source, func(t *testing.T) {
			rec := postPreview(t, map[string]any{
				"type": "smtp",
				"config": map[string]string{
					"from": "ops@example.com", "to": "ops@example.com",
					"template_source": source,
					"template_url":    "http://169.254.169.254/latest/meta-data/",
				},
			})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "inline") {
				t.Errorf("error %q does not say which templates can be previewed", rec.Body.String())
			}
		})
	}
}

func TestPreviewSmtpTemplate_RefusesASinkThatSendsNoEmail(t *testing.T) {
	rec := postPreview(t, map[string]any{
		"type":   "webhook",
		"config": map[string]string{"url": "https://example.com/hook"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestPreviewSmtpTemplate_RefusesAMalformedBody(t *testing.T) {
	rec := httptest.NewRecorder()
	(&SinkHandler{}).PreviewSmtpTemplate(rec, httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/api/sinks/smtp/preview", strings.NewReader("{")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}
