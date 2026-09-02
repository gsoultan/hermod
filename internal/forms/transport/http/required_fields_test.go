package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

// ---------------------------------------------------------------------------
// Required-field validation, and what happens when the field list will not
// parse.
//
// The handler reads the form's field definitions out of the source config,
// unmarshals them, and rejects a submission that leaves a required one empty.
// The unmarshal error was discarded — so malformed stored config produced an
// empty field list, the validation loop ran zero times, and the form accepted
// any payload at all.
//
// A validation bypass that is invisible from the outside: the submission
// succeeds, and the missing fields only surface wherever the data lands.
// ---------------------------------------------------------------------------

// formStore serves one form source.
type formStore struct {
	storage.Storage
	src storage.Source
}

func (f *formStore) ListSources(context.Context, storage.CommonFilter) ([]storage.Source, int, error) {
	return []storage.Source{f.src}, 1, nil
}
func (f *formStore) CreateFormSubmission(context.Context, storage.FormSubmission) error {
	return nil
}
func (f *formStore) CreateLog(context.Context, storage.Log) error           { return nil }
func (f *formStore) CreateAuditLog(context.Context, storage.AuditLog) error { return nil }

func formHandlerWith(fields string) *FormHandler {
	store := &formStore{src: storage.Source{
		ID:   "src-form",
		Type: "form",
		Config: map[string]string{
			"path":                  "/api/forms/test",
			"fields":                fields,
			"enable_bot_protection": "false",
		},
	}}
	return NewFormHandler(&handlers.Handler{Storage: store, LogStorage: store})
}

func submit(t *testing.T, h *FormHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/forms/test", strings.NewReader(body))
	r.SetPathValue("path", "test")
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleForm(rec, r)
	return rec
}

const oneRequiredField = `[{"name":"email","label":"Email","type":"text","required":true}]`

// TestARequiredFieldIsEnforced is the baseline the next test depends on.
func TestARequiredFieldIsEnforced(t *testing.T) {
	rec := submit(t, formHandlerWith(oneRequiredField), `{"other":"value"}`)

	// The success path answers 202, not 200, so this asserts on the class.
	if rec.Code < 400 {
		t.Errorf("a submission missing a required field was accepted (%d)", rec.Code)
	}
}

// TestAMalformedFieldListDoesNotDisableValidation is the bug.
//
// With the unmarshal error discarded, the field list came back empty, the
// validation loop ran zero times, and every submission was accepted — including
// ones missing every field the operator marked required.
func TestAMalformedFieldListDoesNotDisableValidation(t *testing.T) {
	rec := submit(t, formHandlerWith(`[{"name":"email","required":true`), `{}`)

	if rec.Code < 400 {
		t.Errorf("a form whose field definitions could not be parsed accepted a submission "+
			"with nothing in it (%d); malformed config silently turns off every "+
			"required-field check rather than failing", rec.Code)
	}
}

// TestAnEmptyFieldListStillAccepts: a form that genuinely defines no fields is
// not the same as one whose definitions are broken, and must keep working.
func TestAnEmptyFieldListStillAccepts(t *testing.T) {
	rec := submit(t, formHandlerWith(""), `{"anything":"goes"}`)

	if rec.Code >= http.StatusInternalServerError {
		t.Errorf("a form with no field definitions returned %d; defining no fields is a "+
			"valid configuration", rec.Code)
	}
}
