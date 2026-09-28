package http

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/lookup"
)

// A node that writes a field the way the editor names it -- `after.<column>` --
// sat between a source and an api_lookup. The refresh beside AVAILABLE FIELDS
// runs every node, so the lookup received that node's output; its Test button,
// with no run to read from, tested on the source sample and skipped it. Test
// succeeded. The refresh sent
//
//	{"created_by_id": "", "sessions": [{"user_id": "", ... "entity": ""}]}
//
// and the operator's session API refused it as "invalid request body", because
// the write had made a one-field "after" key that every `{{.after.x}}` then
// resolved against. These start where the operator does, at the two editor
// endpoints, and look at what arrived.

// sessionEndpoint decodes a body the way the operator's session API does, into
// uuid.UUID fields, where "" is a decoding error rather than a missing value.
type sessionEndpoint struct {
	mu     sync.Mutex
	bodies []string
}

type sessionRequest struct {
	CreatedByID uuid.UUID `json:"created_by_id"`
	Sessions    []struct {
		UserID   uuid.UUID `json:"user_id"`
		Duration int64     `json:"duration"`
		Scope    struct {
			EntityID uuid.UUID   `json:"entity_id"`
			Entity   string      `json:"entity"`
			RuleIDs  []uuid.UUID `json:"rule_ids"`
		} `json:"scope"`
	} `json:"sessions"`
}

func (e *sessionEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	e.mu.Lock()
	e.bodies = append(e.bodies, string(raw))
	e.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	var req sessionRequest
	if r.Header.Get("Content-Type") != "application/json" || json.Unmarshal(raw, &req) != nil ||
		len(req.Sessions) != 1 || req.Sessions[0].Scope.Entity == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"invalid_argument","message":"invalid request body"}`))
		return
	}
	_, _ = w.Write([]byte(`{"sessions":[{"token":"tok-1"}]}`))
}

func (e *sessionEndpoint) received(t *testing.T) []string {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.bodies...)
}

// The operator's body, verbatim.
const sessionBodyTemplate = `{
  "created_by_id": "{{.after.user_id}}",
  "sessions": [
    {
      "user_id": "{{.after.user_id}}",
      "duration": 604800000000000,
      "scope": {
        "entity_id": "{{.after.entity_id}}",
        "entity": "{{.after.entity_type}}",
        "rule_ids": [
          "019a43e3-f5d7-7bf0-b559-fd3b55215ca7"
        ]
      }
    }
  ]
}`

const (
	sessionUserID   = "07581be3-9ecd-5da5-865e-34ab8aae1fec"
	sessionEntityID = "969176df-27d6-5bb4-9eed-0dbd1ce6a121"
)

// sessionSample is a batch_sql sample as POST /api/sources/sample returns it.
func sessionSample() map[string]any {
	return map[string]any{
		"id":        "sample-query-1790567645",
		"operation": "snapshot",
		"metadata":  map[string]any{"sample": "true"},
		"after": map[string]any{
			"id":           "01a0e612-1148-77d7-b423-cd303d0cdf47",
			"user_id":      sessionUserID,
			"entity_id":    sessionEntityID,
			"entity_type":  "REGISTRATION",
			"status":       "SCHEDULED",
			"scheduled_at": "2026-09-27T22:00:00Z",
			"data":         map[string]any{"RegistrantName": "Seed V2 Tpa Single Day Open"},
		},
	}
}

func TestRefreshSendsTheRowPastANodeThatWritesAnAfterField(t *testing.T) {
	writers := map[string]map[string]any{
		"data_conversion on after.scheduled_at": {
			"transType": "data_conversion", "field": "after.scheduled_at",
			"targetType": "date", "format": "2006-01-02",
		},
		"set on column.after.channel": {"transType": "set", "column.after.channel": "'email'"},
	}

	for name, writer := range writers {
		t.Run(name, func(t *testing.T) {
			endpoint := &sessionEndpoint{}
			srv := httptest.NewServer(endpoint)
			defer srv.Close()

			lookup := map[string]any{
				"label": "Create session", "transType": "api_lookup", "method": "POST",
				"url":     srv.URL + "/v1/sessions",
				"headers": "{\n  \"Content-Type\": \"application/json\"\n}",
				"body":    sessionBodyTemplate, "targetField": "session",
				// Every call reaches the endpoint: a cached response would hide
				// which request each path sent.
				"ttl": "0",
			}

			// The refresh.
			code, steps, raw := postSimulation(t, map[string]any{
				"workflow": map[string]any{
					"name": "reminder sessions",
					"nodes": []map[string]any{
						{"id": "src", "type": "source", "ref_id": "reminders"},
						{"id": "writer", "type": "transformation", "config": writer},
						{"id": "lookup", "type": "transformation", "config": lookup},
					},
					"edges": []map[string]any{
						{"id": "e1", "source_id": "src", "target_id": "writer"},
						{"id": "e2", "source_id": "writer", "target_id": "lookup"},
					},
				},
				"messages": map[string]any{"src": sessionSample()},
				"partial":  true,
			})
			if code != http.StatusOK {
				t.Fatalf("the refresh's run was refused: %d %s", code, raw)
			}
			for _, s := range steps {
				if s["node_id"] == "lookup" && s["error"] != nil {
					t.Errorf("the refresh failed the lookup: %v", s["error"])
				}
			}
			out := stepPayload(steps, "lookup")
			after, _ := out["after"].(map[string]any)
			if after["user_id"] != sessionUserID || after["session"] == nil {
				t.Errorf("the lookup's output lost the row or the response: %v", out)
			}

			// Test API Call, on the source sample, the way the editor sends it
			// before anything has run.
			store := newSQLiteStore(t, "api-test")
			h := &WorkflowHandler{Handler: &handlers.Handler{Storage: store, Registry: registry.NewRegistry(store)}}
			body, _ := json.Marshal(map[string]any{
				"transformation": map[string]any{"type": "api_lookup", "config": lookup},
				"message":        sessionSample(),
			})
			rec := httptest.NewRecorder()
			h.TestTransformation(rec, httptest.NewRequestWithContext(t.Context(),
				http.MethodPost, "/api/transformations/test", bytes.NewReader(body)))
			if rec.Code != http.StatusOK {
				t.Fatalf("Test API Call: %d %s", rec.Code, rec.Body.String())
			}

			got := endpoint.received(t)
			if len(got) != 2 {
				t.Fatalf("the endpoint saw %d requests, want the refresh's and the Test's: %q", len(got), got)
			}
			var sent sessionRequest
			if err := json.Unmarshal([]byte(got[0]), &sent); err != nil {
				t.Fatalf("the refresh sent a body the session API cannot decode (%v):\n%s", err, got[0])
			}
			if sent.CreatedByID.String() != sessionUserID || sent.Sessions[0].Scope.EntityID.String() != sessionEntityID {
				t.Errorf("the refresh sent the wrong row:\n%s", got[0])
			}
			if got[0] != got[1] {
				t.Errorf("the refresh and Test API Call sent different requests:\nrefresh: %s\ntest:    %s", got[0], got[1])
			}
		})
	}
}
