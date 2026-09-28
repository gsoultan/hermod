package lookup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// A refused request was reported as the endpoint's complaint and nothing else:
//
//	api lookup returned status 400: {"code":"invalid_argument","message":"invalid request body"}
//
// The cause was a {{ }} token that found no value and went out as "", which the
// endpoint could only describe as a body it could not decode -- its uuid fields
// do not accept "". api_lookup is the one party that knows which token that was,
// and it said nothing. This is the third time the class has cost a debugging
// session (SQL templates, #171, and a write that hid the after-image), each time
// starting from an error that named the endpoint instead of the token.

func refusingServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"invalid_argument","message":"invalid request body"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runAPILookupOn(t *testing.T, cfg map[string]any, sample map[string]any) error {
	t.Helper()
	tr, reg := newAPIFixture()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	message.PopulateFromMap(msg, sample)
	_, err := tr.Transform(context.WithValue(t.Context(), hermod.RegistryKey, reg), msg, cfg)
	return err
}

func TestAPILookupRefusalNamesTheTokensItSentEmpty(t *testing.T) {
	srv := refusingServer(t)
	cfg := map[string]any{
		"method":      "POST",
		"url":         srv.URL + "/v1/sessions?org={{.after.organization_id}}",
		"headers":     `{"X-Tenant": "{{ .after.tenant }}", "X-Kind": "{{.after.entity_type}}"}`,
		"queryParams": `{"ref": "{{.after.ref}}"}`,
		"body":        `{"created_by_id": "{{.after.user_id}}", "entity": "{{.after.entity_type}}", "again": "{{.after.user_id}}"}`,
		"authType":    "bearer",
		"token":       "{{.after.api_token}}",
		"targetField": "session",
	}
	// Only entity_type has a value; every other token finds nothing.
	err := runAPILookupOn(t, cfg, map[string]any{
		"operation": "snapshot",
		"after":     map[string]any{"entity_type": "REGISTRATION"},
	})
	if err == nil {
		t.Fatal("a refused request reported success")
	}
	got := err.Error()

	if !strings.Contains(got, `"message":"invalid request body"`) {
		t.Errorf("the endpoint's own reason was dropped: %s", got)
	}
	// Each empty token, so the operator can find it in the template -- spaces
	// inside the braces are dropped, the way the resolver reads the path --
	// once each, in the order the request was built.
	want := "{{.after.organization_id}}, {{.after.ref}}, {{.after.tenant}}, {{.after.user_id}}, {{.after.api_token}}"
	if !strings.Contains(got, want) {
		t.Errorf("the error does not name the tokens that went out empty.\n got: %s\nwant it to list: %s", got, want)
	}
	// A token that resolved is not a suspect, and no value is repeated into the
	// error: row data does not belong in a log line.
	if strings.Contains(got, "entity_type") || strings.Contains(got, "REGISTRATION") {
		t.Errorf("the error names a token that had a value, or repeats a value: %s", got)
	}
}

// A refusal with every token bound is the endpoint's business alone, and the
// error stays exactly what it was.
func TestAPILookupRefusalWithEveryTokenBoundAddsNothing(t *testing.T) {
	srv := refusingServer(t)
	err := runAPILookupOn(t, map[string]any{
		"method":      "POST",
		"url":         srv.URL + "/v1/sessions",
		"body":        `{"created_by_id": "{{.after.user_id}}"}`,
		"targetField": "session",
	}, map[string]any{
		"operation": "snapshot",
		"after":     map[string]any{"user_id": "07581be3-9ecd-5da5-865e-34ab8aae1fec"},
	})
	want := `api lookup failed: api lookup returned status 400: {"code":"invalid_argument","message":"invalid request body"}`
	if err == nil || err.Error() != want {
		t.Errorf("error = %v\nwant    %s", err, want)
	}
}
