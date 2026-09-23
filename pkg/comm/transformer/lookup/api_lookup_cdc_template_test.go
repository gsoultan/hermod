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
