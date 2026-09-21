package metis

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// --- an engine that speaks the external-task contract ----------------------

type completion struct {
	taskID    string
	workerID  string
	variables map[string]any
}

type failureReport struct {
	taskID   string
	workerID string
	message  string
	details  string
	retries  float64
}

// taskEngine serves fetch-and-lock, complete and failure the way the real
// server does, including the two refusals it makes on its own: a task held by
// another worker, and a lock that has expired.
type taskEngine struct {
	srv *httptest.Server

	mu               sync.Mutex
	queue            [][]map[string]any // one entry per fetch, in order
	fetches          int
	lastFetch        map[string]any
	completions      []completion
	failures         []failureReport
	held             map[string]string    // task id -> worker holding it
	expiry           map[string]time.Time // task id -> when the lock lapses
	fetchStatus      int                  // non-zero: answer fetch-and-lock with it
	completeAttempts int                  // every call on /complete, refused or not
	logins           int
}

func newTaskEngine(t *testing.T) *taskEngine {
	t.Helper()
	e := &taskEngine{held: map[string]string{}, expiry: map[string]time.Time{}}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := map[string]any{}
		if r.Body != nil {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
		}

		e.mu.Lock()
		defer e.mu.Unlock()

		switch {
		case r.URL.Path == "/api/v1/login":
			e.logins++
			_, _ = w.Write([]byte(`{"token":"tok-1"}`))

		case r.URL.Path == "/api/v1/projects":
			_, _ = w.Write([]byte(`{"projects":[{"id":"proj-1"}]}`))

		case r.URL.Path == "/api/v1/external-tasks/fetch-and-lock":
			e.fetches++
			e.lastFetch = body
			if e.fetchStatus != 0 {
				w.WriteHeader(e.fetchStatus)
				_, _ = w.Write([]byte(`{"error":"nope"}`))
				return
			}
			var batch []map[string]any
			if len(e.queue) > 0 {
				batch, e.queue = e.queue[0], e.queue[1:]
			}
			workerID, _ := body["worker_id"].(string)
			lockMS, _ := body["lock_duration_ms"].(float64)
			for _, task := range batch {
				id, _ := task["id"].(string)
				e.held[id] = workerID
				if _, already := task["lock_expiration"]; !already {
					exp := time.Now().Add(time.Duration(lockMS) * time.Millisecond)
					e.expiry[id] = exp
					task["lock_expiration"] = exp.Format(time.RFC3339Nano)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tasks": batch})

		case strings.HasSuffix(r.URL.Path, "/complete"):
			e.completeAttempts++
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/external-tasks/"), "/complete")
			workerID, _ := body["worker_id"].(string)
			if holder, ok := e.held[id]; ok && holder != workerID {
				_, _ = w.Write([]byte(`{"error":"task is locked by another worker"}`))
				return
			}
			if exp, ok := e.expiry[id]; ok && exp.Before(time.Now()) {
				_, _ = w.Write([]byte(`{"error":"lock has expired"}`))
				return
			}
			vars, _ := body["variables"].(map[string]any)
			e.completions = append(e.completions, completion{taskID: id, workerID: workerID, variables: vars})
			_, _ = w.Write([]byte(`{}`))

		case strings.HasSuffix(r.URL.Path, "/failure"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/external-tasks/"), "/failure")
			workerID, _ := body["worker_id"].(string)
			msg, _ := body["error_message"].(string)
			details, _ := body["error_details"].(string)
			retries, _ := body["retries"].(float64)
			e.failures = append(e.failures, failureReport{
				taskID: id, workerID: workerID, message: msg, details: details, retries: retries,
			})
			_, _ = w.Write([]byte(`{}`))

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(e.srv.Close)
	return e
}

// enqueue schedules one batch of tasks for the next fetch-and-lock.
func (e *taskEngine) enqueue(tasks ...map[string]any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.queue = append(e.queue, tasks)
}

func (e *taskEngine) snapshotCompletions() []completion {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]completion(nil), e.completions...)
}

func (e *taskEngine) fetchCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.fetches
}

func extTask(id string, variables map[string]any) map[string]any {
	return map[string]any{
		"id":               id,
		"topic":            "reverse-charge",
		"retries":          2,
		"node":             map[string]any{"id": "charge"},
		"process_instance": map[string]any{"id": "inst-" + id},
		"variables":        variables,
	}
}

func newTaskSourceForTest(t *testing.T, e *taskEngine, mutate func(*ExternalTaskConfig)) *ExternalTaskSource {
	t.Helper()
	cfg := ExternalTaskConfig{
		BaseURL:      e.srv.URL,
		Token:        "tok",
		Topic:        "reverse-charge",
		WorkerID:     "hermod-test",
		PollInterval: time.Millisecond,
		LockDuration: time.Minute,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	src, err := NewExternalTask(cfg)
	if err != nil {
		t.Fatalf("NewExternalTask: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })
	return src
}

func readOne(t *testing.T, src *ExternalTaskSource) hermod.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	msg, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if msg == nil {
		t.Fatal("Read returned no message")
	}
	return msg
}

// --- reading ----------------------------------------------------------------

func TestRead_LocksATaskAndCarriesItsVariablesAndIdentity(t *testing.T) {
	e := newTaskEngine(t)
	e.enqueue(extTask("task-1", map[string]any{"amount": 500.0, "currency": "EUR"}))
	src := newTaskSourceForTest(t, e, nil)

	msg := readOne(t, src)

	if msg.ID() != "task-1" {
		t.Errorf("message id: want task-1, got %q", msg.ID())
	}
	if got := msg.Data()["amount"]; got != 500.0 {
		t.Errorf("the step's variables should be the message's data: want amount=500, got %v", got)
	}
	meta := msg.Metadata()
	for key, want := range map[string]string{
		"metis_task_id":     "task-1",
		"metis_topic":       "reverse-charge",
		"metis_worker_id":   "hermod-test",
		"metis_instance_id": "inst-task-1",
		"metis_node_id":     "charge",
		"source":            "metis",
	} {
		if meta[key] != want {
			t.Errorf("metadata %s: want %q, got %q", key, want, meta[key])
		}
	}
	if meta["metis_lock_expiration"] == "" {
		t.Error("the lock expiry must ride in metadata; Ack has nothing to check without it")
	}
}

// A task's own variables must not be shadowed by the envelope. The identity
// goes in metadata precisely so a process variable called `id` or `topic`
// survives the trip.
func TestRead_TheEnvelopeDoesNotOverwriteAProcessVariable(t *testing.T) {
	e := newTaskEngine(t)
	e.enqueue(extTask("task-1", map[string]any{"id": "ORD-9", "topic": "customer-chosen"}))
	src := newTaskSourceForTest(t, e, nil)

	msg := readOne(t, src)

	if got := msg.Data()["id"]; got != "ORD-9" {
		t.Errorf("the process's own id variable was overwritten: want ORD-9, got %v", got)
	}
	if got := msg.Data()["topic"]; got != "customer-chosen" {
		t.Errorf("the process's own topic variable was overwritten: want customer-chosen, got %v", got)
	}
}

func TestRead_HandsOutEveryLockedTaskBeforeFetchingAgain(t *testing.T) {
	e := newTaskEngine(t)
	e.enqueue(extTask("task-1", nil), extTask("task-2", nil), extTask("task-3", nil))
	src := newTaskSourceForTest(t, e, nil)

	for i, want := range []string{"task-1", "task-2", "task-3"} {
		if got := readOne(t, src).ID(); got != want {
			t.Fatalf("message %d: want %s, got %s", i, want, got)
		}
	}
	if got := e.fetchCount(); got != 1 {
		t.Errorf("one batch should cost one fetch, got %d", got)
	}
}

func TestFetchAndLock_SendsTheConfiguredBatchAndLock(t *testing.T) {
	e := newTaskEngine(t)
	e.enqueue(extTask("task-1", nil))
	src := newTaskSourceForTest(t, e, func(c *ExternalTaskConfig) {
		c.MaxTasks = 3
		c.LockDuration = 90 * time.Second
	})

	readOne(t, src)

	e.mu.Lock()
	defer e.mu.Unlock()
	if got := e.lastFetch["topic"]; got != "reverse-charge" {
		t.Errorf("topic: want reverse-charge, got %v", got)
	}
	if got := e.lastFetch["max_tasks"]; got != 3.0 {
		t.Errorf("max_tasks: want 3, got %v", got)
	}
	if got := e.lastFetch["lock_duration_ms"]; got != 90000.0 {
		t.Errorf("lock_duration_ms: want 90000, got %v", got)
	}
}

// --- acknowledging = completing ---------------------------------------------

func TestAck_CompletesTheTaskWithWhatThePipelineProduced(t *testing.T) {
	e := newTaskEngine(t)
	e.enqueue(extTask("task-1", map[string]any{"amount": 500.0}))
	src := newTaskSourceForTest(t, e, nil)

	msg := readOne(t, src)
	// What the pipeline did: the whole point of the integration.
	msg.SetData("reversed", true)
	msg.SetData("reference", "RV-77")

	if err := src.Ack(t.Context(), msg); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	got := e.snapshotCompletions()
	if len(got) != 1 {
		t.Fatalf("want one completion, got %d", len(got))
	}
	if got[0].taskID != "task-1" || got[0].workerID != "hermod-test" {
		t.Errorf("completed the wrong task or as the wrong worker: %+v", got[0])
	}
	if got[0].variables["reversed"] != true || got[0].variables["reference"] != "RV-77" {
		t.Errorf("the pipeline's output never reached the process: %v", got[0].variables)
	}
	if got[0].variables["amount"] != 500.0 {
		t.Errorf("the step's input should still be there: %v", got[0].variables)
	}
}

func TestAck_VariableFieldsNarrowsWhatGoesBackToTheProcess(t *testing.T) {
	e := newTaskEngine(t)
	e.enqueue(extTask("task-1", map[string]any{"amount": 500.0, "card_number": "4111111111111111"}))
	src := newTaskSourceForTest(t, e, func(c *ExternalTaskConfig) {
		c.VariableFields = []string{"reversed"}
	})

	msg := readOne(t, src)
	msg.SetData("reversed", true)
	if err := src.Ack(t.Context(), msg); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	vars := e.snapshotCompletions()[0].variables
	if vars["reversed"] != true {
		t.Errorf("the selected field should be sent: %v", vars)
	}
	if _, leaked := vars["card_number"]; leaked {
		t.Errorf("an unselected field was written back into the process: %v", vars)
	}
}

// The lock is the whole safety model: past it, another worker may hold this
// task, and completing it would let two workers finish the same step.
func TestAck_RefusesToCompleteOnceTheLockHasExpired(t *testing.T) {
	e := newTaskEngine(t)
	expired := extTask("task-1", nil)
	expired["lock_expiration"] = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
	e.enqueue(expired)
	src := newTaskSourceForTest(t, e, nil)

	msg := readOne(t, src)
	err := src.Ack(t.Context(), msg)

	if err == nil {
		t.Fatal("completing a task whose lock has lapsed must be refused, not attempted")
	}
	if !strings.Contains(err.Error(), "lock") {
		t.Errorf("the error should say the lock lapsed, got %v", err)
	}
	if got := e.snapshotCompletions(); len(got) != 0 {
		t.Errorf("nothing should have been sent, got %+v", got)
	}
	// The engine refuses an expired lock too, so a test that only checked for
	// an error would pass with no guard here at all. What this pins is that the
	// doomed round trip is never made and the reason is this source's own.
	e.mu.Lock()
	attempts := e.completeAttempts
	e.mu.Unlock()
	if attempts != 0 {
		t.Errorf("the completion was sent anyway (%d attempts); the lock check is not doing anything", attempts)
	}
	if !strings.Contains(err.Error(), "lock_duration") {
		t.Errorf("the error should say how to stop it happening again, got %v", err)
	}
}

// A message whose identity a transformation stripped cannot be completed. Going
// quiet there would leave the task to expire and be redone forever, with
// nothing in the log saying why.
func TestAck_SaysSoWhenTheTaskIdentityWasStripped(t *testing.T) {
	e := newTaskEngine(t)
	e.enqueue(extTask("task-1", nil))
	src := newTaskSourceForTest(t, e, nil)

	msg := readOne(t, src)
	msg.SetMetadata("metis_task_id", "")

	if err := src.Ack(t.Context(), msg); err == nil {
		t.Fatal("want an error naming the stripped identity, got nil")
	}
}

func TestAck_SurfacesTheEnginesRefusal(t *testing.T) {
	e := newTaskEngine(t)
	e.enqueue(extTask("task-1", nil))
	src := newTaskSourceForTest(t, e, nil)
	msg := readOne(t, src)

	// Somebody else now holds it.
	e.mu.Lock()
	e.held["task-1"] = "another-worker"
	e.mu.Unlock()

	if err := src.Ack(t.Context(), msg); err == nil {
		t.Fatal("the engine refused the completion; Ack must not report success")
	}
}

// --- configuration -----------------------------------------------------------

func TestNewExternalTask_RequiresATopic(t *testing.T) {
	_, err := NewExternalTask(ExternalTaskConfig{BaseURL: "https://bpm.example.com", Token: "t"})
	if err == nil || !strings.Contains(err.Error(), "topic") {
		t.Fatalf("want a refusal naming the topic, got %v", err)
	}
}

func TestNewExternalTask_RefusesPlaintextOffLoopback(t *testing.T) {
	_, err := NewExternalTask(ExternalTaskConfig{BaseURL: "http://bpm.example.com", Token: "t", Topic: "x"})
	if err == nil || !strings.Contains(err.Error(), "plaintext") {
		t.Fatalf("want a refusal naming plaintext http, got %v", err)
	}
}

// Two sources sharing a worker id can complete each other's tasks — the server
// authorises a completion by that id alone. An unset one must therefore be
// unique, not a constant.
func TestNewExternalTask_GeneratesADistinctWorkerIDWhenUnset(t *testing.T) {
	one, err := NewExternalTask(ExternalTaskConfig{BaseURL: "https://bpm.example.com", Token: "t", Topic: "x"})
	if err != nil {
		t.Fatalf("NewExternalTask: %v", err)
	}
	two, err := NewExternalTask(ExternalTaskConfig{BaseURL: "https://bpm.example.com", Token: "t", Topic: "x"})
	if err != nil {
		t.Fatalf("NewExternalTask: %v", err)
	}
	if one.WorkerID() == "" {
		t.Fatal("a worker id is required by the protocol; an unset one must be generated")
	}
	if one.WorkerID() == two.WorkerID() {
		t.Errorf("two sources share the worker id %q; either could complete the other's tasks", one.WorkerID())
	}
}

// There is no cursor to persist: what a restart resumes from is the engine's
// own leases. Reporting state would invite a caller to restore a position that
// means nothing here.
func TestExternalTaskSource_DoesNotPersistState(t *testing.T) {
	e := newTaskEngine(t)
	src := newTaskSourceForTest(t, e, nil)
	if _, ok := any(src).(hermod.Stateful); ok {
		t.Error("the external-task source must not be Stateful; its position lives in the engine's locks")
	}
}

func TestExternalTaskSource_PingReachesTheEngine(t *testing.T) {
	e := newTaskEngine(t)
	src := newTaskSourceForTest(t, e, nil)
	if err := src.Ping(t.Context()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestExternalTaskSource_LogsInWhenGivenAPassword(t *testing.T) {
	e := newTaskEngine(t)
	e.enqueue(extTask("task-1", nil))
	src := newTaskSourceForTest(t, e, func(c *ExternalTaskConfig) {
		c.Token = ""
		c.Username = "svc"
		c.Password = "pw"
	})

	readOne(t, src)

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.logins == 0 {
		t.Error("a source holding a password should log in rather than call unauthenticated")
	}
}

// --- what the engine calls an acknowledgement is not always a success --------

// Hermod acknowledges a message it could not deliver but did preserve: a node
// that failed, or a sink that refused, parks the message in the dead-letter
// sink and then acks so the source stops replaying it. For every other source
// that is right. Here it would complete the BPMN step, and the process would
// carry on to its next node believing work happened that in fact sits in a
// dead-letter queue.
func TestAck_FailsTheTaskWhenThePipelineDeadLetteredIt(t *testing.T) {
	e := newTaskEngine(t)
	e.enqueue(extTask("task-1", nil))
	src := newTaskSourceForTest(t, e, nil)

	msg := readOne(t, src)
	msg.SetMetadata("_hermod_dead_lettered", "true")
	msg.SetMetadata("_hermod_last_error", "sink refused: connection reset")

	if err := src.Ack(t.Context(), msg); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	if got := e.snapshotCompletions(); len(got) != 0 {
		t.Fatalf("a dead-lettered message must not complete the step: %+v", got)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.failures) != 1 {
		t.Fatalf("want the task reported failed, got %d reports", len(e.failures))
	}
	if !strings.Contains(e.failures[0].message, "connection reset") {
		t.Errorf("the reason the pipeline gave should reach the operator: %q", e.failures[0].message)
	}
	// The pipeline already decided this message cannot be delivered and kept a
	// copy. Handing back a retry would have the engine redeliver it into the
	// same pipeline, which fails the same way; spending them raises the
	// incident an operator can actually act on.
	if e.failures[0].retries != 0 {
		t.Errorf("a dead-lettered task should not be retried by the engine, got retries=%v", e.failures[0].retries)
	}
}

func TestAck_FailsTheTaskWhenValidationRejectedIt(t *testing.T) {
	e := newTaskEngine(t)
	e.enqueue(extTask("task-1", nil))
	src := newTaskSourceForTest(t, e, nil)

	msg := readOne(t, src)
	msg.SetMetadata("_hermod_validation_failed", "true")
	msg.SetMetadata("_hermod_last_error", "amount: must be a number")

	if err := src.Ack(t.Context(), msg); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	if got := e.snapshotCompletions(); len(got) != 0 {
		t.Fatalf("a message that failed validation must not complete the step: %+v", got)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.failures) != 1 {
		t.Fatalf("want the task reported failed, got %d reports", len(e.failures))
	}
	if !strings.Contains(e.failures[0].message, "must be a number") {
		t.Errorf("the validation error should reach the operator: %q", e.failures[0].message)
	}
}

// The ordinary path must stay ordinary: a message carrying neither marker is a
// success and completes the step.
func TestAck_CompletesWhenNoMarkerSaysOtherwise(t *testing.T) {
	e := newTaskEngine(t)
	e.enqueue(extTask("task-1", nil))
	src := newTaskSourceForTest(t, e, nil)

	if err := src.Ack(t.Context(), readOne(t, src)); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if got := e.snapshotCompletions(); len(got) != 1 {
		t.Fatalf("want one completion, got %d", len(got))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.failures) != 0 {
		t.Errorf("nothing failed; want no failure report, got %+v", e.failures)
	}
}

// The sink-outage park is the one that matters and the one that is easiest to
// miss: the engine stamps only `_hermod_failed_at` on it. A guard reading
// `_hermod_dead_lettered` alone completes the step after every sink outage.
func TestAck_FailsTheTaskWhenOnlyTheParkTimestampSaysItWasParked(t *testing.T) {
	e := newTaskEngine(t)
	e.enqueue(extTask("task-1", nil))
	src := newTaskSourceForTest(t, e, nil)

	msg := readOne(t, src)
	msg.SetMetadata("_hermod_failed_at", time.Now().Format(time.RFC3339))
	msg.SetMetadata("_hermod_last_error", "connection reset by peer")

	if err := src.Ack(t.Context(), msg); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	if got := e.snapshotCompletions(); len(got) != 0 {
		t.Fatalf("a parked message must not complete the step: %+v", got)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.failures) != 1 {
		t.Fatalf("want the task reported failed, got %d reports", len(e.failures))
	}
	if !strings.Contains(e.failures[0].message, "connection reset") {
		t.Errorf("the sink's error should reach the operator: %q", e.failures[0].message)
	}
}

// --- pacing ------------------------------------------------------------------

// A busy topic is exactly when pacing costs throughput: the batch just drained
// is evidence there is more waiting. The SDK's own worker refetches immediately
// after a non-empty batch and only sleeps when a fetch found nothing, and this
// source matches it — pacing every fetch caps a topic at MaxTasks per interval
// however fast the pipeline runs.
func TestRead_RefetchesImmediatelyAfterABatchThatHadWork(t *testing.T) {
	e := newTaskEngine(t)
	e.enqueue(extTask("task-1", nil))
	e.enqueue(extTask("task-2", nil))
	src := newTaskSourceForTest(t, e, func(c *ExternalTaskConfig) {
		// Long enough that waiting it out is indistinguishable from hanging.
		c.PollInterval = time.Hour
	})

	if got := readOne(t, src).ID(); got != "task-1" {
		t.Fatalf("first read: want task-1, got %s", got)
	}
	if got := readOne(t, src).ID(); got != "task-2" {
		t.Fatalf("second read: want task-2, got %s", got)
	}
	if got := e.fetchCount(); got != 2 {
		t.Errorf("want two fetches, got %d", got)
	}
}

// A fetch that found nothing is paced, or an idle topic becomes a hot loop
// against the engine.
func TestRead_PausesAfterAFetchThatFoundNothing(t *testing.T) {
	e := newTaskEngine(t)
	e.enqueue()
	e.enqueue(extTask("task-1", nil))
	src := newTaskSourceForTest(t, e, func(c *ExternalTaskConfig) {
		c.PollInterval = 200 * time.Millisecond
	})

	start := time.Now()
	readOne(t, src)
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Errorf("an empty fetch should be followed by a pause of about the poll interval, waited %v", elapsed)
	}
}
