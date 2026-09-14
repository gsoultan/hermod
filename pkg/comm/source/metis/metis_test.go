package metis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// --- a fake engine -------------------------------------------------------------

// engine serves the three listings the source polls. Tests set the rows; the
// handler pages and orders them the way the real server does — newest first.
type engine struct {
	srv *httptest.Server

	mu        sync.Mutex
	instances []map[string]any
	tasks     []map[string]any
	incidents map[string][]map[string]any
	calls     []string
	fail      int // status to answer listings with, when non-zero
}

func newEngine(t *testing.T) *engine {
	t.Helper()
	e := &engine{incidents: map[string][]map[string]any{}}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.calls = append(e.calls, r.URL.Path)
		fail := e.fail
		e.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/login":
			_, _ = w.Write([]byte(`{"token":"tok-1"}`))
		case r.URL.Path == "/api/v1/projects":
			_, _ = w.Write([]byte(`{"projects":[{"id":"proj-1"}]}`))
		case r.URL.Path == "/api/v1/instances":
			if fail != 0 {
				w.WriteHeader(fail)
				return
			}
			e.mu.Lock()
			rows := e.instances
			e.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"instances": rows, "page": map[string]any{"has_more": false}})
		case r.URL.Path == "/api/v1/tasks":
			if fail != 0 {
				w.WriteHeader(fail)
				return
			}
			e.mu.Lock()
			rows := e.tasks
			e.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"tasks": rows, "page": map[string]any{"has_more": false}})
		case strings.HasPrefix(r.URL.Path, "/api/v1/incidents/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/v1/incidents/")
			e.mu.Lock()
			rows := e.incidents[id]
			e.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"incidents": rows})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *engine) url() string { return e.srv.URL }

func (e *engine) countPath(path string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, c := range e.calls {
		if c == path {
			n++
		}
	}
	return n
}

// at is a fixed clock so the tests read as an ordering rather than a race.
func at(minute int) string {
	return time.Date(2026, 9, 12, 10, minute, 0, 0, time.UTC).Format(time.RFC3339Nano)
}

// instance builds one row as the server sends it. The listing is newest first,
// so tests pass them in descending order.
func instance(id string, minute int, status string) map[string]any {
	return map[string]any{
		"id":         id,
		"status":     status,
		"created_at": at(minute),
		"variables":  map[string]any{"amount": 900},
		"definition": map[string]any{"id": "def-1", "key": "order-fulfilment", "name": "Order fulfilment"},
	}
}

func task(id string, minute int, status string) map[string]any {
	return map[string]any{
		"id":         id,
		"name":       "Approve order",
		"status":     status,
		"created_at": at(minute),
		"instance":   map[string]any{"id": "inst-1"},
	}
}

func incident(id, instanceID string, minute int) map[string]any {
	return map[string]any{
		"id":         id,
		"error":      "connector timed out",
		"status":     "open",
		"created_at": at(minute),
		"instance":   map[string]any{"id": instanceID},
		"node":       map[string]any{"id": "ServiceTask_1"},
	}
}

func baseConfig(url string) Config {
	return Config{
		BaseURL:      url,
		Token:        "tok-static",
		ProjectID:    "proj-1",
		Stream:       StreamInstances,
		PollInterval: time.Millisecond,
	}
}

// drain reads n messages with a deadline, so a wedged source fails the test
// rather than hanging it.
func drain(t *testing.T, s *Source, n int) []hermod.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out := make([]hermod.Message, 0, n)
	for range n {
		msg, err := s.Read(ctx)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		out = append(out, msg)
	}
	return out
}

func ids(msgs []hermod.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.ID())
	}
	return out
}

// --- configuration ---------------------------------------------------------------

func TestNew_NamesTheMissingSetting(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"no base url", func(c *Config) { c.BaseURL = "" }, "base url"},
		{"no project", func(c *Config) { c.ProjectID = "" }, "project id"},
		{"no credential", func(c *Config) { c.Token = "" }, "token"},
		{"unknown stream", func(c *Config) { c.Stream = "everything" }, "stream"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseConfig("https://bpm.example.com")
			tc.mut(&cfg)
			if _, err := New(cfg); err == nil {
				t.Fatal("New accepted a config it cannot work with")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %q", err, tc.want)
			}
		})
	}
}

func TestNew_RefusesPlaintextOffLoopback(t *testing.T) {
	if _, err := New(baseConfig("http://bpm.example.com")); err == nil {
		t.Fatal("plaintext to a remote host was accepted; the token travels in a header")
	}
	if _, err := New(baseConfig("http://127.0.0.1:8080")); err != nil {
		t.Fatalf("plaintext to loopback should be allowed: %v", err)
	}
}

// --- reading ------------------------------------------------------------------

// The server answers newest first. A pipeline wants them in the order they
// happened, so the source reverses the page.
func TestRead_EmitsOldestFirst(t *testing.T) {
	e := newEngine(t)
	e.instances = []map[string]any{
		instance("inst-3", 30, "completed"),
		instance("inst-2", 20, "completed"),
		instance("inst-1", 10, "completed"),
	}

	s, err := New(baseConfig(e.url()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	got := ids(drain(t, s, 3))
	want := []string{"inst-1", "inst-2", "inst-3"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("read %v, want %v — the pipeline should see them in the order they happened", got, want)
		}
	}
}

func TestRead_CarriesTheInstanceOntoTheMessage(t *testing.T) {
	e := newEngine(t)
	e.instances = []map[string]any{instance("inst-1", 10, "completed")}

	s, err := New(baseConfig(e.url()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	msg := drain(t, s, 1)[0]
	if msg.ID() != "inst-1" {
		t.Errorf("ID = %q", msg.ID())
	}
	if msg.Table() != string(StreamInstances) {
		t.Errorf("Table = %q, want %q", msg.Table(), StreamInstances)
	}
	if got := msg.Data()["status"]; got != "completed" {
		t.Errorf("data.status = %v, want completed", got)
	}
	if got := msg.Data()["definition_key"]; got != "order-fulfilment" {
		t.Errorf("data.definition_key = %v, want order-fulfilment", got)
	}
	vars, ok := msg.Data()["variables"].(map[string]any)
	if !ok {
		t.Fatalf("data.variables is %T, want the instance's variables", msg.Data()["variables"])
	}
	if vars["amount"] != 900.0 {
		t.Errorf("variables.amount = %v", vars["amount"])
	}
	if msg.Metadata()["metis_stream"] != string(StreamInstances) {
		t.Errorf("metadata.metis_stream = %q", msg.Metadata()["metis_stream"])
	}
	if msg.Metadata()["metis_created_at"] == "" {
		t.Error("the message carries no watermark, so Ack has nothing to advance")
	}
}

func TestRead_DoesNotRepeatARowAcrossPolls(t *testing.T) {
	e := newEngine(t)
	e.instances = []map[string]any{instance("inst-1", 10, "completed")}

	s, err := New(baseConfig(e.url()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	first := drain(t, s, 1)[0]
	if err := s.Ack(context.Background(), first); err != nil {
		t.Fatal(err)
	}

	// A second row arrives; the first must not come back with it.
	e.mu.Lock()
	e.instances = []map[string]any{instance("inst-2", 20, "completed"), instance("inst-1", 10, "completed")}
	e.mu.Unlock()

	second := drain(t, s, 1)[0]
	if second.ID() != "inst-2" {
		t.Errorf("read %q again, want inst-2 — the first row was already delivered", second.ID())
	}
}

// Two rows can share a created_at. A cursor that is only a timestamp drops the
// second one, so the source remembers which ids it has already passed at the
// instant it is sitting on.
func TestRead_TiesAtOneTimestampAreNotDropped(t *testing.T) {
	e := newEngine(t)
	e.instances = []map[string]any{
		instance("inst-b", 10, "completed"),
		instance("inst-a", 10, "completed"),
	}

	s, err := New(baseConfig(e.url()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	got := ids(drain(t, s, 2))
	if len(got) != 2 || got[0] == got[1] {
		t.Fatalf("read %v, want both rows at the shared timestamp", got)
	}
}

func TestRead_ReturnsWithinTheContextDeadline(t *testing.T) {
	e := newEngine(t) // no rows at all: the source has nothing to hand over

	cfg := baseConfig(e.url())
	cfg.PollInterval = time.Hour // and nothing to wake it before the deadline
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { _, err := s.Read(ctx); done <- err }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Read returned a message from an empty engine")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Read ignored the context deadline")
	}
}

func TestRead_SurfacesAListingFailure(t *testing.T) {
	e := newEngine(t)
	e.fail = http.StatusInternalServerError

	s, err := New(baseConfig(e.url()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := s.Read(ctx); err == nil {
		t.Fatal("a failing listing should surface, not look like an idle poll")
	}
}

// --- the cursor: the bug class this repo has shipped ten times -------------------

// Read must not move the position a restart resumes from. Only Ack means the
// row arrived somewhere.
func TestAck_AdvancesTheCursorAndReadDoesNot(t *testing.T) {
	e := newEngine(t)
	e.instances = []map[string]any{
		instance("inst-3", 30, "completed"),
		instance("inst-2", 20, "completed"),
		instance("inst-1", 10, "completed"),
	}

	s, err := New(baseConfig(e.url()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	msgs := drain(t, s, 3)
	if got := s.GetState()["last_at"]; got != "" {
		t.Fatalf("three rows read and none acknowledged, but the cursor moved to %q", got)
	}

	// Acknowledge only the first. A restart must come back for the other two.
	if err := s.Ack(context.Background(), msgs[0]); err != nil {
		t.Fatal(err)
	}
	if got := s.GetState()["last_at"]; got != at(10) {
		t.Fatalf("cursor = %q, want %q — the position of the one row acknowledged", got, at(10))
	}
}

func TestAck_NilIsNotAPanic(t *testing.T) {
	e := newEngine(t)
	s, err := New(baseConfig(e.url()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.Ack(context.Background(), nil); err != nil {
		t.Fatalf("Ack(nil): %v", err)
	}
	if got := s.GetState()["last_at"]; got != "" {
		t.Errorf("a nil ack moved the cursor to %q", got)
	}
}

func TestAck_WithoutAWatermarkLeavesTheCursorAlone(t *testing.T) {
	e := newEngine(t)
	e.instances = []map[string]any{instance("inst-1", 10, "completed")}

	s, err := New(baseConfig(e.url()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	msg := drain(t, s, 1)[0]
	if err := s.Ack(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	before := s.GetState()["last_at"]

	// A message from somewhere else in the pipeline, carrying no watermark.
	foreign := &strippedMessage{id: "not-ours"}
	if err := s.Ack(context.Background(), foreign); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if after := s.GetState()["last_at"]; after != before {
		t.Errorf("cursor moved from %q to %q on a message with no watermark", before, after)
	}
}

func TestAck_DoesNotMoveTheCursorBackwards(t *testing.T) {
	e := newEngine(t)
	e.instances = []map[string]any{
		instance("inst-2", 20, "completed"),
		instance("inst-1", 10, "completed"),
	}

	s, err := New(baseConfig(e.url()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	msgs := drain(t, s, 2)
	// Acknowledged out of order, as a concurrent worker pool would.
	if err := s.Ack(context.Background(), msgs[1]); err != nil {
		t.Fatal(err)
	}
	if err := s.Ack(context.Background(), msgs[0]); err != nil {
		t.Fatal(err)
	}

	if got := s.GetState()["last_at"]; got != at(20) {
		t.Errorf("cursor = %q, want %q — an older ack must not rewind it", got, at(20))
	}
}

func TestSetState_ResumesFromTheAckedPosition(t *testing.T) {
	e := newEngine(t)
	e.instances = []map[string]any{
		instance("inst-3", 30, "completed"),
		instance("inst-2", 20, "completed"),
		instance("inst-1", 10, "completed"),
	}

	s, err := New(baseConfig(e.url()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.SetState(map[string]string{"last_at": at(20), "last_ids": "inst-2"})

	got := ids(drain(t, s, 1))
	if got[0] != "inst-3" {
		t.Errorf("resumed at %q, want inst-3 — everything up to inst-2 was already delivered", got[0])
	}
}

func TestGetState_RoundTripsThroughSetState(t *testing.T) {
	e := newEngine(t)
	e.instances = []map[string]any{instance("inst-1", 10, "completed")}

	first, err := New(baseConfig(e.url()))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	msg := drain(t, first, 1)[0]
	if err := first.Ack(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	saved := first.GetState()

	second, err := New(baseConfig(e.url()))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	second.SetState(saved)

	if got := second.GetState()["last_at"]; got != saved["last_at"] {
		t.Errorf("state did not survive the round trip: %q vs %q", got, saved["last_at"])
	}
}

// --- the other two streams -------------------------------------------------------

func TestRead_TasksStream(t *testing.T) {
	e := newEngine(t)
	e.tasks = []map[string]any{task("task-2", 20, "unclaimed"), task("task-1", 10, "claimed")}

	cfg := baseConfig(e.url())
	cfg.Stream = StreamTasks
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	got := drain(t, s, 2)
	if got[0].ID() != "task-1" {
		t.Errorf("read %q first, want task-1", got[0].ID())
	}
	if got[0].Data()["name"] != "Approve order" {
		t.Errorf("data.name = %v", got[0].Data()["name"])
	}
	if got[0].Data()["instance_id"] != "inst-1" {
		t.Errorf("data.instance_id = %v, want inst-1", got[0].Data()["instance_id"])
	}
	if n := e.countPath("/api/v1/tasks"); n == 0 {
		t.Error("the tasks stream never listed tasks")
	}
}

// Incidents have no project-wide listing, so the source finds the failed
// instances first and asks each one for its incidents.
func TestRead_IncidentsStream(t *testing.T) {
	e := newEngine(t)
	e.instances = []map[string]any{
		instance("inst-2", 20, "failed"),
		instance("inst-1", 10, "completed"),
	}
	e.incidents["inst-2"] = []map[string]any{incident("inc-1", "inst-2", 21)}

	cfg := baseConfig(e.url())
	cfg.Stream = StreamIncidents
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	msg := drain(t, s, 1)[0]
	if msg.ID() != "inc-1" {
		t.Errorf("ID = %q, want inc-1", msg.ID())
	}
	if msg.Data()["error"] != "connector timed out" {
		t.Errorf("data.error = %v", msg.Data()["error"])
	}
	if msg.Data()["instance_id"] != "inst-2" {
		t.Errorf("data.instance_id = %v, want inst-2", msg.Data()["instance_id"])
	}
	if msg.Data()["node_id"] != "ServiceTask_1" {
		t.Errorf("data.node_id = %v, want ServiceTask_1", msg.Data()["node_id"])
	}

	// The completed instance is not asked for incidents: it has none by
	// definition, and asking is a request per instance per poll.
	if n := e.countPath("/api/v1/incidents/inst-1"); n != 0 {
		t.Errorf("asked a completed instance for incidents %d times", n)
	}
}

// --- reachability -----------------------------------------------------------------

func TestPing_ListsProjects(t *testing.T) {
	e := newEngine(t)
	s, err := New(baseConfig(e.url()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if n := e.countPath("/api/v1/projects"); n != 1 {
		t.Errorf("Ping made %d project listings, want 1", n)
	}
}

func TestClose_IsIdempotent(t *testing.T) {
	e := newEngine(t)
	s, err := New(baseConfig(e.url()))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestLogin_HappensOnceWithCredentials(t *testing.T) {
	e := newEngine(t)
	e.instances = []map[string]any{instance("inst-1", 10, "completed")}

	cfg := baseConfig(e.url())
	cfg.Token = ""
	cfg.Username = "svc"
	cfg.Password = "secret"
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	drain(t, s, 1)
	if n := e.countPath("/api/v1/login"); n != 1 {
		t.Errorf("logged in %d times, want 1", n)
	}
}

// --- a message carrying no metis metadata ------------------------------------------

type strippedMessage struct{ id string }

func (m *strippedMessage) ID() string                     { return m.id }
func (m *strippedMessage) Operation() hermod.Operation    { return hermod.OpCreate }
func (m *strippedMessage) Table() string                  { return "" }
func (m *strippedMessage) Schema() string                 { return "" }
func (m *strippedMessage) Before() []byte                 { return nil }
func (m *strippedMessage) After() []byte                  { return nil }
func (m *strippedMessage) Payload() []byte                { return nil }
func (m *strippedMessage) Metadata() map[string]string    { return map[string]string{} }
func (m *strippedMessage) MetadataRef() map[string]string { return map[string]string{} }
func (m *strippedMessage) Data() map[string]any           { return map[string]any{} }
func (m *strippedMessage) DataRef() map[string]any        { return map[string]any{} }
func (m *strippedMessage) SetMetadata(string, string)     {}
func (m *strippedMessage) SetData(string, any)            {}
func (m *strippedMessage) Clone() hermod.Message          { return m }
func (m *strippedMessage) ToMap() map[string]any          { return map[string]any{} }
func (m *strippedMessage) ClearPayloads()                 {}
func (m *strippedMessage) Retain()                        {}
func (m *strippedMessage) Release()                       {}
