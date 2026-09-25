package lookup

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// ---------------------------------------------------------------------------
// api_lookup resolved every one of its templates -- url, body, headers, query
// params, credentials -- with evaluator.ResolveTemplate against msg.Data(),
// which walks the data map literally. A CDC message's data map *is* its
// after-image, so {{.after.user_id}} had nothing to walk and resolved to the
// empty string. The request still went out, with "" where the id belonged, and
// the endpoint answered 400 -- reported as "api lookup returned status 400",
// which describes the endpoint rather than the token that was never bound.
//
// This is the defect fixed for SQL templates in 4f1993d (sqlutil resolved paths
// through the data map, not the message). api_lookup is the same shape.
// ---------------------------------------------------------------------------

// cdcSample is the editor sample shape for a CDC insert: the row columns live
// under "after", exactly as the Test panel posts them.
func cdcSample() map[string]any {
	return map[string]any{
		"operation": "insert",
		"table":     "memberships",
		"schema":    "public",
		"after": map[string]any{
			"user_id":     "019a43e3-0000-7000-8000-000000000001",
			"entity_id":   "019a43e3-0000-7000-8000-000000000002",
			"entity_type": "organization",
		},
	}
}

// capturingServer records the request body it was handed, so an unbound token
// is visible as the text that went out rather than inferred from a status code.
func capturingServer(t *testing.T) (*httptest.Server, func() string) {
	t.Helper()
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"session_id": "sess-1"})
	}))
	t.Cleanup(srv.Close)
	return srv, func() string { return gotBody }
}

// apiLookupCDC runs one node against the CDC sample. The request the endpoint
// received is what these tests assert on, so only the error comes back.
func apiLookupCDC(t *testing.T, cfg map[string]any) error {
	t.Helper()

	tr, reg := newAPIFixture()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	message.PopulateFromMap(msg, cdcSample())

	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	_, err := tr.Transform(ctx, msg, cfg)
	return err
}

// The reported case, with the operator's body verbatim: three {{.after.X}}
// tokens in a JSON body posted to a session endpoint.
func TestAPILookupBodyResolvesCDCEnvelopePaths(t *testing.T) {
	srv, captured := capturingServer(t)

	body := `{
  "created_by_id": "{{.after.user_id}}",
  "sessions": [
    {
      "user_id": "{{.after.user_id}}",
      "duration": 604800000000000,
      "scope": {
        "entity_id": "{{.after.entity_id}}",
        "entity": "{{.after.entity_type}}",
        "rule_ids": ["019a43e3-f5d7-7bf0-b559-fd3b55215ca7"]
      }
    }
  ]
}`

	if err := apiLookupCDC(t, map[string]any{
		"method":       "POST",
		"url":          srv.URL + "/sessions",
		"body":         body,
		"responsePath": "session_id",
		"targetField":  "session_id",
	}); err != nil {
		t.Fatalf("Transform: %v", err)
	}

	sent := captured()
	var got map[string]any
	if err := json.Unmarshal([]byte(sent), &got); err != nil {
		t.Fatalf("request body is not JSON (%v): %s", err, sent)
	}

	if got["created_by_id"] != "019a43e3-0000-7000-8000-000000000001" {
		t.Errorf("created_by_id = %#v, want the after-image user_id; body sent:\n%s",
			got["created_by_id"], sent)
	}
	sessions, ok := got["sessions"].([]any)
	if !ok || len(sessions) != 1 {
		t.Fatalf("sessions = %#v, want one entry", got["sessions"])
	}
	s, _ := sessions[0].(map[string]any)
	if s["user_id"] != "019a43e3-0000-7000-8000-000000000001" {
		t.Errorf("sessions[0].user_id = %#v, want the after-image user_id", s["user_id"])
	}
	scope, _ := s["scope"].(map[string]any)
	if scope["entity_id"] != "019a43e3-0000-7000-8000-000000000002" {
		t.Errorf("scope.entity_id = %#v, want the after-image entity_id", scope["entity_id"])
	}
	if scope["entity"] != "organization" {
		t.Errorf("scope.entity = %#v, want %q", scope["entity"], "organization")
	}
}

// The body is not the only template on this node. The URL, the query params,
// the headers and the bearer token are resolved by the same call and were all
// blind to the envelope in the same way -- an empty path segment or an empty
// Authorization header reads as an endpoint problem too.
func TestAPILookupURLHeadersAndQueryResolveCDCEnvelopePaths(t *testing.T) {
	var gotPath, gotQuery, gotEntityHdr, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotEntityHdr = r.Header.Get("X-Entity-Id")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"session_id": "sess-2"})
	}))
	t.Cleanup(srv.Close)

	if err := apiLookupCDC(t, map[string]any{
		"method":       "GET",
		"url":          srv.URL + "/users/{{.after.user_id}}",
		"queryParams":  `{"entity":"{{.after.entity_type}}"}`,
		"headers":      `{"X-Entity-Id":"{{.after.entity_id}}"}`,
		"authType":     "bearer",
		"token":        "{{.after.user_id}}",
		"responsePath": "session_id",
		"targetField":  "session_id",
	}); err != nil {
		t.Fatalf("Transform: %v", err)
	}

	if want := "/users/019a43e3-0000-7000-8000-000000000001"; gotPath != want {
		t.Errorf("request path = %q, want %q", gotPath, want)
	}
	if want := "entity=organization"; gotQuery != want {
		t.Errorf("request query = %q, want %q", gotQuery, want)
	}
	if want := "019a43e3-0000-7000-8000-000000000002"; gotEntityHdr != want {
		t.Errorf("X-Entity-Id = %q, want %q", gotEntityHdr, want)
	}
	if want := "Bearer 019a43e3-0000-7000-8000-000000000001"; gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
}

// Templates that the data map already answers must keep working: the fix moves
// resolution to the message, and the message answers the data map first.
func TestAPILookupPlainDataPathsStillResolve(t *testing.T) {
	srv, calls := echoServer(t, "tenant", func(r *http.Request) string {
		return strings.TrimPrefix(r.URL.Path, "/t/")
	})
	tr, reg := newAPIFixture()

	got := apiLookup(t, tr, reg, map[string]any{
		"method":       "GET",
		"url":          srv.URL + "/t/{{.tenant}}",
		"responsePath": "tenant",
		"targetField":  "who",
	}, map[string]any{"tenant": "acme"})

	if got["who"] != "acme" {
		t.Errorf("who = %#v, want %q", got["who"], "acme")
	}
	if calls() != 1 {
		t.Errorf("calls = %d, want 1", calls())
	}
}

// ---------------------------------------------------------------------------
// With the tokens bound, the reported body still failed, and the request it
// sent showed why. It went out with no Content-Type at all, so an endpoint that
// requires application/json -- most JSON APIs -- refused it; and a template was
// pasted in as raw text, so a jsonb field in quotes arrived as its JSON text
// inside a string (`"profile": "{"name":"Ada"}"`, not JSON at all) and any
// value holding a quote broke the body the same way. The refusal came back as
// "api lookup returned status 400" and nothing else.
// ---------------------------------------------------------------------------

// sentRequest is what an endpoint was handed.
type sentRequest struct {
	body        string
	contentType string
}

// recordingServer answers with status and reply, and records every request.
func recordingServer(t *testing.T, status int, reply string) (string, func() sentRequest) {
	t.Helper()
	var got sentRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = sentRequest{body: string(b), contentType: r.Header.Get("Content-Type")}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() sentRequest { return got }
}

// jsonbSample is a CDC row with a jsonb column and a value holding a quote.
func jsonbSample() map[string]any {
	return map[string]any{
		"operation": "snapshot",
		"table":     "memberships",
		"after": map[string]any{
			"user_id":     "019a43e3-0000-7000-8000-000000000001",
			"entity_type": `region "north"`,
			"profile":     map[string]any{"name": "Ada", "roles": []any{"owner", "admin"}},
		},
	}
}

func apiLookupRow(t *testing.T, row map[string]any, cfg map[string]any) error {
	t.Helper()
	tr, reg := newAPIFixture()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	message.PopulateFromMap(msg, row)
	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	_, err := tr.Transform(ctx, msg, cfg)
	return err
}

func TestAPILookupSendsAJSONBodyAsJSON(t *testing.T) {
	url, sent := recordingServer(t, http.StatusCreated, `{"id":"sess-1"}`)

	if err := apiLookupRow(t, jsonbSample(), map[string]any{
		"method": "POST", "url": url, "targetField": "session", "ttl": "0",
		"body": `{"profile": "{{.after.profile}}", "entity": "{{.after.entity_type}}"}`,
	}); err != nil {
		t.Fatalf("Transform: %v", err)
	}

	got := sent()
	if got.contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json: a JSON body without it is refused by "+
			"an endpoint that checks", got.contentType)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("the endpoint was sent something that is not JSON (%v): %s", err, got.body)
	}
	profile, ok := body["profile"].(map[string]any)
	if !ok {
		t.Fatalf("profile arrived as %T %#v, want the jsonb object itself", body["profile"], body["profile"])
	}
	if profile["name"] != "Ada" {
		t.Errorf("profile = %#v", profile)
	}
	if body["entity"] != `region "north"` {
		t.Errorf("entity = %#v, want %q", body["entity"], `region "north"`)
	}
}

// The operator's own Content-Type is the one that goes out.
func TestAPILookupKeepsAConfiguredContentType(t *testing.T) {
	url, sent := recordingServer(t, http.StatusOK, `{"id":"sess-1"}`)

	if err := apiLookupRow(t, jsonbSample(), map[string]any{
		"method": "POST", "url": url, "targetField": "session", "ttl": "0",
		"headers": `{"content-type":"application/vnd.api+json"}`,
		"body":    `{"user": "{{.after.user_id}}"}`,
	}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got := sent().contentType; got != "application/vnd.api+json" {
		t.Errorf("Content-Type = %q, want the configured one", got)
	}
}

// A GET has no body, and nothing to describe.
func TestAPILookupSendsNoContentTypeWithoutABody(t *testing.T) {
	url, sent := recordingServer(t, http.StatusOK, `{"id":"sess-1"}`)

	if err := apiLookupRow(t, jsonbSample(), map[string]any{
		"method": "GET", "url": url + "/users/{{.after.user_id}}", "targetField": "session", "ttl": "0",
	}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got := sent().contentType; got != "" {
		t.Errorf("Content-Type = %q on a request with no body", got)
	}
}

// A refusal has to say what the endpoint objected to, or the operator is left
// with a status code and a body they cannot see.
func TestAPILookupErrorCarriesWhatTheEndpointSaid(t *testing.T) {
	url, _ := recordingServer(t, http.StatusUnprocessableEntity,
		`{"error":"scope.entity_id must be a uuid"}`)

	err := apiLookupRow(t, jsonbSample(), map[string]any{
		"method": "POST", "url": url, "targetField": "session", "ttl": "0", "onMiss": "fail",
		"body": `{"user": "{{.after.user_id}}"}`,
	})
	if err == nil {
		t.Fatal("a 422 was not reported as a failure")
	}
	for _, want := range []string{"422", "scope.entity_id must be a uuid"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not carry %q", err, want)
		}
	}
}
