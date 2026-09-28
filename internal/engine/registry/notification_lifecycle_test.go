package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/notification"
	"github.com/gsoultan/hermod/internal/storage"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// withAlerting attaches a recording notification service to a registry built by
// another harness, so the supervisor and shutdown paths can be asserted on.
func withAlerting(r *Registry, wf storage.Workflow) *mockNotificationProvider {
	ms := &mockAlertingStorage{workflow: wf}
	if r.storage == nil {
		r.storage = ms
	}
	provider := &mockNotificationProvider{}
	ns := notification.NewService(ms)
	ns.AddProvider(provider)
	r.notificationService = ns
	return provider
}

// "Automatic recovery is exhausted; manual intervention required" is the single
// most alert-worthy line the system produces: supervision has stopped and the
// workflow will not come back on its own. It was written to the log and
// nowhere else.
func TestSupervisorExhaustionNotifies(t *testing.T) {
	wf := storage.Workflow{ID: "wf-1", Name: "orders"}
	h := newSupervisorHarness(t)
	provider := withAlerting(h.reg, wf)

	// Spend the budget, then stall once more to hit the refusal. The attempts
	// have to be spaced past the settle window (which grows to 8m) and still
	// fall inside stallRestartWindow, or allow() refuses for settling rather
	// than for exhaustion and the wrong branch is exercised.
	now := time.Now()
	for i := maxStallRestarts; i > 0; i-- {
		h.reg.supervisor.allow("wf-1", now.Add(-time.Duration(i)*9*time.Minute))
	}
	h.reg.superviseStall("wf-1", wf, "sink unreachable for 10m")

	titles, messages := settled(h.reg, provider)
	if !slices.Contains(titles, "Workflow Recovery Exhausted") {
		t.Fatalf("titles = %v, want a Workflow Recovery Exhausted alert", titles)
	}
	joined := strings.Join(messages, " ")
	if !strings.Contains(joined, "orders") {
		t.Errorf("message = %q, want it to name the workflow", joined)
	}
	if !strings.Contains(joined, "sink unreachable for 10m") {
		t.Errorf("message = %q, want it to carry the stall reason", joined)
	}
}

// A rebuild that itself fails leaves the workflow stopped. That is terminal for
// this episode and has to be said out loud.
func TestSupervisorFailedRebuildNotifies(t *testing.T) {
	wf := storage.Workflow{ID: "wf-1", Name: "orders"}
	h := newSupervisorHarness(t)
	provider := withAlerting(h.reg, wf)

	h.mu.Lock()
	h.failNext = context.DeadlineExceeded
	h.mu.Unlock()

	h.reg.superviseStall("wf-1", wf, "no message completed in 63s")

	titles, _ := settled(h.reg, provider)
	if !slices.Contains(titles, "Workflow Restart Failed") {
		t.Errorf("titles = %v, want a Workflow Restart Failed alert", titles)
	}
}

// A successful automatic restart must not alert: the supervisor did its job.
// Alerting here would make every transient stall page someone.
func TestSupervisorSuccessfulRestartDoesNotNotify(t *testing.T) {
	wf := storage.Workflow{ID: "wf-1", Name: "orders"}
	h := newSupervisorHarness(t)
	provider := withAlerting(h.reg, wf)

	h.reg.superviseStall("wf-1", wf, "no message completed in 63s")

	titles, _ := settled(h.reg, provider)
	for _, ti := range titles {
		if ti == "Workflow Recovery Exhausted" || ti == "Workflow Restart Failed" {
			t.Errorf("a successful automatic restart alerted %q", ti)
		}
	}
}

// Item 4: a worker going down takes every workflow on it with it. Nothing
// anywhere said so — StopAll and Close were silent.
func TestWorkerShutdownNotifies(t *testing.T) {
	r := &Registry{
		engines: make(map[string]*activeEngine),
		logger:  telemetry.NewDefaultLogger(),
	}
	provider := withAlerting(r, storage.Workflow{ID: "", Name: "worker"})
	r.workerID = "worker-7"

	r.NotifyWorkerShutdown(context.Background(), 3)

	titles, messages := settled(r, provider)
	if !slices.Contains(titles, "Worker Shutting Down") {
		t.Fatalf("titles = %v, want a Worker Shutting Down alert", titles)
	}
	joined := strings.Join(messages, " ")
	if !strings.Contains(joined, "worker-7") {
		t.Errorf("message = %q, want it to name the worker", joined)
	}
	if !strings.Contains(joined, "3") {
		t.Errorf("message = %q, want it to say how many workflows are affected", joined)
	}
}

// One alert for the worker, not one per workflow it happened to be running.
func TestWorkerShutdownAlertsOncePerWorker(t *testing.T) {
	r := &Registry{
		engines: make(map[string]*activeEngine),
		logger:  telemetry.NewDefaultLogger(),
	}
	provider := withAlerting(r, storage.Workflow{ID: "", Name: "worker"})
	r.workerID = "worker-7"

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.NotifyWorkerShutdown(context.Background(), 3)
		}()
	}
	wg.Wait()

	titles, _ := settled(r, provider)
	if got := len(titles); got != 1 {
		t.Errorf("sent %d worker-shutdown alerts (%v), want 1", got, titles)
	}
}

// Item 4: an operator stopping a workflow is a state change worth reporting —
// setStatus("Stopped") matched none of the alert predicates.
func TestOperatorStopNotifies(t *testing.T) {
	wf := storage.Workflow{ID: "wf-1", Name: "orders"}
	r := &Registry{
		engines: make(map[string]*activeEngine),
		logger:  telemetry.NewDefaultLogger(),
	}
	provider := withAlerting(r, wf)

	r.notifyWorkflowStopped(context.Background(), "wf-1")

	titles, messages := settled(r, provider)
	if !slices.Contains(titles, "Workflow Stopped") {
		t.Fatalf("titles = %v, want a Workflow Stopped alert", titles)
	}
	if !strings.Contains(strings.Join(messages, " "), "orders") {
		t.Errorf("message = %v, want it to name the workflow", messages)
	}
}

// The lifecycle alerts must not be filed as errors: they would land in the UI's
// error view and add ERROR rows — which the database logger never samples — to
// a log table that is already the largest thing in the schema.
func TestLifecycleAlertsUseInfoSeverity(t *testing.T) {
	wf := storage.Workflow{ID: "wf-1", Name: "orders"}

	t.Run("operator stop", func(t *testing.T) {
		r := &Registry{engines: make(map[string]*activeEngine), logger: telemetry.NewDefaultLogger()}
		provider := withAlerting(r, wf)
		r.notifyWorkflowStopped(context.Background(), "wf-1")
		settled(r, provider)

		if got := provider.sentLevels(); len(got) != 1 || got[0] != notification.LevelInfo {
			t.Errorf("levels = %v, want [INFO]", got)
		}
	})

	t.Run("worker shutdown", func(t *testing.T) {
		r := &Registry{engines: make(map[string]*activeEngine), logger: telemetry.NewDefaultLogger()}
		provider := withAlerting(r, wf)
		r.NotifyWorkerShutdown(context.Background(), 2)
		settled(r, provider)

		if got := provider.sentLevels(); len(got) != 1 || got[0] != notification.LevelInfo {
			t.Errorf("levels = %v, want [INFO]", got)
		}
	})

	t.Run("a real fault stays an error", func(t *testing.T) {
		r, provider := newAlertingRegistry(t, wf)
		var latch atomic.Bool
		r.notifyOnStatusChange(t.Context(), "wf-1", telemetry.StatusUpdate{
			EngineStatus: "Error: sink ping failed",
		}, &latch)
		settled(r, provider)

		if got := provider.sentLevels(); len(got) != 1 || got[0] != notification.LevelError {
			t.Errorf("levels = %v, want [ERROR]", got)
		}
	})
}

// A fresh install builds its registry before it has a database: main.go passes
// nil storage on a first run, and FinalizeInitialSetup attaches the database
// afterwards with SetStorage. The alert channels were registered only when
// NewRegistry was handed storage, and SetStorage updates the channels that
// exist -- none -- so a freshly set-up Hermod alerted nowhere, not its
// configured channels and not its own UI, until the process restarted.
// Measured live on v1.15.0: a workflow went to Error, its status said so, and
// no alert arrived.

// alertChannel is a webhook alert channel that records what it is sent.
func alertChannel(t *testing.T) (*httptest.Server, <-chan string) {
	t.Helper()
	received := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- string(body)
	}))
	t.Cleanup(srv.Close)
	return srv, received
}

// setUpStore is the database first-run setup hands the registry: real SQLite,
// holding the alert channels an operator configured.
func setUpStore(t *testing.T, webhookURL string) storage.Storage {
	t.Helper()
	db, err := sql.Open("sqlite", "file:fresh_"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := sqlstorage.NewSQLStorage(db, "sqlite")
	if err := store.Init(t.Context()); err != nil {
		t.Fatalf("init store: %v", err)
	}
	settings, err := json.Marshal(notification.NotificationSettings{WebhookURL: webhookURL})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	if err := store.SaveSetting(t.Context(), "notification_settings", string(settings)); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	return store
}

func delivered(t *testing.T, reg *Registry, received <-chan string) []string {
	t.Helper()
	if !reg.notificationService.WaitFor(5 * time.Second) {
		t.Fatal("alerts were still being sent after 5s")
	}
	var bodies []string
	for {
		select {
		case b := <-received:
			bodies = append(bodies, b)
		default:
			return bodies
		}
	}
}

func TestAFreshInstallAlertsWithoutARestart(t *testing.T) {
	channel, received := alertChannel(t)

	reg := NewRegistry(nil) // a first run: no database yet
	store := setUpStore(t, channel.URL)
	reg.SetStorage(store) // what FinalizeInitialSetup does

	wf := storage.Workflow{ID: "wf-fresh", Name: "orders"}
	reg.notificationService.Notify(t.Context(), "Workflow Error", "orders entered error state: sink unreachable", wf)

	bodies := delivered(t, reg, received)
	if len(bodies) == 0 || !strings.Contains(bodies[0], "sink unreachable") {
		t.Errorf("the webhook channel received %q; a freshly set-up install must alert without a restart", bodies)
	}

	logs, _, err := store.ListLogs(t.Context(), storage.LogFilter{
		CommonFilter: storage.CommonFilter{Limit: 10}, WorkflowID: "wf-fresh", Action: "NOTIFICATION",
	})
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	if len(logs) == 0 {
		t.Error("no NOTIFICATION log: the alert never reached the UI either")
	}
}

// With the channels in place before a database exists, an alert raised in that
// window has nothing to read settings from and nowhere to record itself. It must
// be dropped -- not dereference the missing database -- and must not count
// against the five-minute repeat window, or the first real alert after setup
// would be swallowed as a duplicate.
func TestAnAlertBeforeSetupNeitherPanicsNorSilencesTheNextOne(t *testing.T) {
	channel, received := alertChannel(t)
	reg := NewRegistry(nil)
	wf := storage.Workflow{ID: "wf-fresh", Name: "orders"}

	reg.notificationService.Notify(t.Context(), "Workflow Error", "raised before setup", wf)
	if got := delivered(t, reg, received); len(got) != 0 {
		t.Fatalf("an alert with no database delivered %q", got)
	}

	reg.SetStorage(setUpStore(t, channel.URL))
	reg.notificationService.Notify(t.Context(), "Workflow Error", "raised after setup", wf)

	bodies := delivered(t, reg, received)
	if len(bodies) != 1 || !strings.Contains(bodies[0], "raised after setup") {
		t.Errorf("after setup the channel received %q, want the one alert raised after setup", bodies)
	}
}
