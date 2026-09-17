package http

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// storage.Source.State is written on every update — sqlStorage.UpdateSource
// does `json.Marshal(src.State)` unconditionally — so a request body that never
// mentions `state` decoded to a nil map and overwrote the column with null.
//
// Two UI callers build exactly that body: useSourceForm.onSampleReady, which
// fires on every Test Connection, and useSourceForm.submitMutation, which fires
// on every wizard save (it carefully carries `sample` across and never mentions
// `state`). Both therefore reset a batch_sql `last_value` watermark or a
// Postgres CDC cursor as a side effect of an unrelated action, and nothing in
// the request or the response said so: the damage only shows up on the next
// scheduled run, as rows that were already delivered arriving again.
//
// The bundle-import path in internal/workflow/transport/http/workflow.go
// already restores `src.State = existing.State` for this reason — "rewinds or
// fast-forwards a live CDC cursor, which loses or replays everything in
// between". UpdateSource fetches oldSrc too, and now does the same.
//
// The bodies here are decoded from JSON rather than built as Go values on
// purpose: the whole question is what `encoding/json` leaves behind for a field
// the client did not send, and a hand-set nil would assume the answer.
func decodeSourceBody(t *testing.T, body string) storage.Source {
	t.Helper()
	var src storage.Source
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&src); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return src
}

func TestUpdateSourceKeepsTheCursorAnUpdateDidNotMention(t *testing.T) {
	stored := storage.Source{
		ID: "nightly", Name: "nightly-orders", Type: "batch_sql",
		State: map[string]string{"last_value": "4211"},
	}

	cases := []struct {
		name string
		body string
		want map[string]string
	}{
		{
			// What Test Connection and the source wizard actually send.
			name: "a body with no state keeps the stored cursor",
			body: `{"id":"nightly","name":"nightly-orders","type":"batch_sql","vhost":"default","active":true,"config":{},"sample":"{}"}`,
			want: map[string]string{"last_value": "4211"},
		},
		{
			// Indistinguishable from omission after decoding, and the safe
			// reading of both is "I am not telling you about state".
			name: "an explicit null keeps it too",
			body: `{"id":"nightly","name":"nightly-orders","type":"batch_sql","state":null}`,
			want: map[string]string{"last_value": "4211"},
		},
		{
			// The one way to actually clear a cursor: say so with an object.
			name: "an empty object clears it",
			body: `{"id":"nightly","name":"nightly-orders","type":"batch_sql","state":{}}`,
			want: map[string]string{},
		},
		{
			name: "a supplied cursor wins",
			body: `{"id":"nightly","name":"nightly-orders","type":"batch_sql","state":{"last_value":"9000"}}`,
			want: map[string]string{"last_value": "9000"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := carryRuntimeColumns(decodeSourceBody(t, tc.body), stored)

			if len(got.State) != len(tc.want) {
				t.Fatalf("state = %v, want %v", got.State, tc.want)
			}
			for k, v := range tc.want {
				if got.State[k] != v {
					t.Errorf("state[%q] = %q, want %q", k, got.State[k], v)
				}
			}
		})
	}
}

// The stored source carrying no cursor is the ordinary case and must not
// invent one.
func TestUpdateSourceLeavesAnAbsentCursorAbsent(t *testing.T) {
	stored := storage.Source{ID: "nightly", Name: "nightly-orders", Type: "batch_sql"}
	body := `{"id":"nightly","name":"nightly-orders","type":"batch_sql","config":{}}`

	if got := carryRuntimeColumns(decodeSourceBody(t, body), stored); got.State != nil {
		t.Fatalf("state = %v, want nil", got.State)
	}
}

// carryRuntimeColumns must not become a general "keep whatever you left out":
// the configuration fields are the point of the update.
func TestUpdateSourceStillAppliesTheConfigurationItWasGiven(t *testing.T) {
	stored := storage.Source{
		ID: "nightly", Name: "old name", Type: "batch_sql", VHost: "default",
		State: map[string]string{"last_value": "4211"},
	}
	body := `{"id":"nightly","name":"new name","type":"batch_sql","vhost":"reporting","active":true,"config":{"cron":"0 5 * * *"}}`

	got := carryRuntimeColumns(decodeSourceBody(t, body), stored)

	if got.Name != "new name" {
		t.Errorf("name = %q, want %q", got.Name, "new name")
	}
	if got.VHost != "reporting" {
		t.Errorf("vhost = %q, want %q", got.VHost, "reporting")
	}
	if got.Config["cron"] != "0 5 * * *" {
		t.Errorf("config[cron] = %q, want %q", got.Config["cron"], "0 5 * * *")
	}
	if got.State["last_value"] != "4211" {
		t.Errorf("the cursor was lost while applying a config edit: %v", got.State)
	}
}
