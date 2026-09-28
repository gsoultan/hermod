package advanced_test

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/advanced"
)

// A `set` column's value can be a JSON object -- `column.after.QueryParams`
// holding `{"session": "source.after.session.sessions.0.access_token"}` is the
// shape an api_lookup's query parameters want. Only a *string* value was ever
// evaluated: anything else was written through as it was configured, so the
// object reached the message with the path text in it instead of the token,
// and the request built from it went out with "source.after..." as the
// session. The editor's own hint ("Use {{.field}} to reference incoming data")
// led to the braced spelling, which no value resolved either.

// setNodeCDCRow is one CDC row shaped like the scheduler event the report came
// from: a nested list of sessions inside the row.
func setNodeCDCRow(t *testing.T) hermod.Message {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetOperation(hermod.OpSnapshot)
	msg.SetData("status", "SCHEDULED")
	msg.SetData("note", "{{env.HERMOD_SET_NODE_TEST_SECRET}}")
	msg.SetData("label", "source.status")
	msg.SetData("session", map[string]any{
		"sessions": []any{map[string]any{"access_token": "tok-1", "id": "s-1"}},
	})
	return msg
}

// runSetNode runs a stored `set` config through the registry. prepared is the
// engine's path (Prepare once, then Transform); unprepared is the one the
// editor's Test and Live Preview take.
func runSetNode(t *testing.T, cfg map[string]any, msg hermod.Message, prepared bool) hermod.Message {
	t.Helper()
	tr, ok := transformer.Get("set")
	if !ok {
		t.Fatal("no transformer is registered as set")
	}
	if prepared {
		p, ok := tr.(transformer.PreparedTransformer)
		if !ok {
			t.Fatal("set is not a PreparedTransformer")
		}
		var err error
		if cfg, err = p.Prepare(cfg); err != nil {
			t.Fatalf("Prepare: %v", err)
		}
	}
	out, err := tr.Transform(t.Context(), msg, cfg)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	return out
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %#v: %v", v, err)
	}
	return string(b)
}

var setNodePaths = []struct {
	name     string
	prepared bool
}{
	{"engine", true},
	{"editor preview", false},
}

func TestSetNodeResolvesReferencesInsideAValue(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{
			name:  "a source path inside an object",
			value: map[string]any{"session": "source.after.session.sessions.0.access_token"},
			want:  `{"session":"tok-1"}`,
		},
		{
			name:  "source paths inside an array",
			value: []any{"source.status", "source.after.session.sessions.0.id"},
			want:  `["SCHEDULED","s-1"]`,
		},
		{
			name:  "a source path two objects down",
			value: map[string]any{"auth": map[string]any{"token": "source.after.session.sessions.0.access_token"}},
			want:  `{"auth":{"token":"tok-1"}}`,
		},
		{
			name:  "a braced source path",
			value: map[string]any{"session": "{{source.after.session.sessions.0.access_token}}"},
			want:  `{"session":"tok-1"}`,
		},
		{
			name:  "a dotted template token",
			value: map[string]any{"session": "{{ .after.session.sessions.0.access_token }}"},
			want:  `{"session":"tok-1"}`,
		},
		{
			name:  "a token inside text",
			value: map[string]any{"Authorization": "Bearer {{.after.session.sessions.0.access_token}}"},
			want:  `{"Authorization":"Bearer tok-1"}`,
		},
		{
			name:  "a whole token keeps an object an object",
			value: map[string]any{"first": "{{source.after.session.sessions.0}}"},
			want:  `{"first":{"access_token":"tok-1","id":"s-1"}}`,
		},
		{
			name:  "a function inside a token",
			value: map[string]any{"status": "{{lower(source.status)}}"},
			want:  `{"status":"scheduled"}`,
		},
		{
			name:  "a token in a plain string value",
			value: "Bearer {{.after.session.sessions.0.access_token}}",
			want:  `"Bearer tok-1"`,
		},
		{
			name:  "a braced source path as a plain string value",
			value: "{{source.after.status}}",
			want:  `"SCHEDULED"`,
		},
	}
	for _, path := range setNodePaths {
		for _, tc := range tests {
			t.Run(path.name+"/"+tc.name, func(t *testing.T) {
				cfg := map[string]any{"transType": "set", "column.after.QueryParams": tc.value}
				out := runSetNode(t, cfg, setNodeCDCRow(t), path.prepared)
				if got := jsonOf(t, out.Data()["QueryParams"]); got != tc.want {
					t.Errorf("QueryParams = %s, want %s", got, tc.want)
				}
			})
		}
	}
}

// Text inside an object is literal unless it asks to be read: a JSON document
// already says which values are numbers and which are text, so "007" has to
// stay "007" and "Paris (France)" must not be called as a function named Paris.
func TestSetNodeKeepsLiteralsInsideAValue(t *testing.T) {
	value := map[string]any{
		"code":    "007",
		"version": "1.0",
		"enabled": "true",
		"city":    "Paris (France)",
		"quoted":  "'x'",
		"count":   42,
		"active":  true,
		"none":    nil,
		"tags":    []any{"a", "lower(b)"},
	}
	want := `{"active":true,"city":"Paris (France)","code":"007","count":42,"enabled":"true","none":null,"quoted":"'x'","tags":["a","lower(b)"],"version":"1.0"}`
	for _, path := range setNodePaths {
		t.Run(path.name, func(t *testing.T) {
			cfg := map[string]any{"transType": "set", "column.after.meta": value}
			out := runSetNode(t, cfg, setNodeCDCRow(t), path.prepared)
			if got := jsonOf(t, out.Data()["meta"]); got != want {
				t.Errorf("meta = %s\n want %s", got, want)
			}
		})
	}
}

// The configured object is the node's, for every message. Written into the
// message by reference, a later node adding a field to it added that field to
// the config -- so the next message through the first node carried a value
// from the previous one, and two messages in flight wrote one map at once.
func TestSetNodeDoesNotHandItsConfiguredObjectToTheMessage(t *testing.T) {
	first := map[string]any{"transType": "set", "column.after.QueryParams": map[string]any{"kind": "reminder"}}
	second := map[string]any{"transType": "set", "column.after.QueryParams.status": "source.status"}
	configured := jsonOf(t, first["column.after.QueryParams"])

	msg := runSetNode(t, maps.Clone(first), setNodeCDCRow(t), true)
	msg = runSetNode(t, second, msg, true)
	if got := jsonOf(t, msg.Data()["QueryParams"]); got != `{"kind":"reminder","status":"SCHEDULED"}` {
		t.Fatalf("after both nodes QueryParams = %s", got)
	}

	if got := jsonOf(t, first["column.after.QueryParams"]); got != configured {
		t.Errorf("the second node wrote into the first node's config: %s, configured %s", got, configured)
	}
	next := runSetNode(t, maps.Clone(first), setNodeCDCRow(t), true)
	if got := jsonOf(t, next.Data()["QueryParams"]); got != `{"kind":"reminder"}` {
		t.Errorf("the next message's QueryParams = %s, want {\"kind\":\"reminder\"}", got)
	}
}

// Resolving tokens in a value must not open what every other template in
// Hermod closes: {{env.X}} reads nothing, and a resolved value is written as
// data, never read again as a token or an expression.
func TestSetNodeValueTokensDoNotReachTheEnvironment(t *testing.T) {
	t.Setenv("HERMOD_SET_NODE_TEST_SECRET", "s3cret-value")
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{
			name:  "an env token in an object",
			value: map[string]any{"k": "{{env.HERMOD_SET_NODE_TEST_SECRET}}"},
			want:  `{"k":null}`,
		},
		{
			name:  "an env token in text",
			value: "key={{env.HERMOD_SET_NODE_TEST_SECRET}}",
			want:  `"key="`,
		},
		{
			name:  "a row value that looks like a token",
			value: map[string]any{"n": "{{.after.note}}"},
			want:  `{"n":"{{env.HERMOD_SET_NODE_TEST_SECRET}}"}`,
		},
		{
			name:  "a row value that looks like a source path",
			value: map[string]any{"l": "{{.after.label}}"},
			want:  `{"l":"source.status"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{"transType": "set", "column.after.out": tc.value}
			out := runSetNode(t, cfg, setNodeCDCRow(t), true)
			got := jsonOf(t, out.Data()["out"])
			if strings.Contains(got, "s3cret-value") {
				t.Fatalf("out = %s: an environment variable reached the message", got)
			}
			if got != tc.want {
				t.Errorf("out = %s, want %s", got, tc.want)
			}
		})
	}
}

// The advanced node evaluates its columns with the same evaluator, so an
// object value resolves there too.
func TestAdvancedNodeResolvesReferencesInsideAValue(t *testing.T) {
	tr, ok := transformer.Get("advanced")
	if !ok {
		t.Fatal("no transformer is registered as advanced")
	}
	cfg := map[string]any{
		"transType":          "advanced",
		"column.QueryParams": map[string]any{"session": "source.after.session.sessions.0.access_token"},
	}
	out, err := tr.Transform(t.Context(), setNodeCDCRow(t), cfg)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got := jsonOf(t, out.Data()["QueryParams"]); got != `{"session":"tok-1"}` {
		t.Errorf("QueryParams = %s, want {\"session\":\"tok-1\"}", got)
	}
}

// The report's own sample, through the path the editor's Test button takes:
// the sample map is populated into a message and the node's unprepared config
// runs against it; what comes back is the message's map form, as JSON -- its
// `after` is raw JSON text until it is encoded.
func TestSetNodeResolvesAnObjectValueOnTheEditorSample(t *testing.T) {
	session := map[string]any{
		"sessions": []any{map[string]any{"access_token": "tok-1", "id": "s-1"}},
	}
	sample := map[string]any{
		"after": map[string]any{
			"id":      "01a0e612-1148-77d7-b423-cd303d0cdf47",
			"status":  "SCHEDULED",
			"session": session,
		},
		"id":        "01a0e612-1148-77d7-b423-cd303d0cdf47",
		"metadata":  map[string]any{"sample": "true"},
		"operation": "snapshot",
		"status":    "SCHEDULED",
		"session":   session,
	}
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	message.PopulateFromMap(msg, sample)

	cfg := map[string]any{
		"transType":                "set",
		"column.after.QueryParams": map[string]any{"session": "source.after.session.sessions.0.access_token"},
	}
	out := runSetNode(t, cfg, msg, false)

	var reply struct {
		After map[string]json.RawMessage `json:"after"`
	}
	if err := json.Unmarshal([]byte(jsonOf(t, out.ToMap())), &reply); err != nil {
		t.Fatalf("decode the message's map form: %v", err)
	}
	if got := string(reply.After["QueryParams"]); got != `{"session":"tok-1"}` {
		t.Errorf("after.QueryParams = %s, want {\"session\":\"tok-1\"}", got)
	}
}
