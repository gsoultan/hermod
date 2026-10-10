package monitor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// logStore is a prediction log store in memory.
type logStore struct {
	mu   sync.Mutex
	rows []storage.MLPredictionLog
	err  error
}

func (s *logStore) InsertMLPredictionLogs(_ context.Context, logs []storage.MLPredictionLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.rows = append(s.rows, logs...)
	return nil
}
func (s *logStore) ListMLPredictionLogs(context.Context, string, string, int) ([]storage.MLPredictionLog, error) {
	return nil, nil
}
func (s *logStore) PurgeMLPredictionLogs(context.Context, string, string, time.Time) error {
	return nil
}
func (s *logStore) DeleteMLPredictionLogs(context.Context, string, string) error { return nil }

func (s *logStore) logged() []storage.MLPredictionLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storage.MLPredictionLog(nil), s.rows...)
}

// trainedStats answers the training stats of every version, and counts asks.
type trainedStats struct {
	mu    sync.Mutex
	asked int
	err   error
	stats map[string]worker.FeatureStats
}

func (t *trainedStats) get(_ context.Context, _, _, _ string) (map[string]worker.FeatureStats, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.asked++
	return t.stats, t.err
}

type notified struct {
	mu      sync.Mutex
	reports []Report
}

func (n *notified) notify(_ context.Context, r Report) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.reports = append(n.reports, r)
}

func (n *notified) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.reports)
}

var testConfig = Config{Window: time.Minute, MinRows: 100, Queue: 64, Batch: 1000, Flush: time.Hour}

func newTestMonitor(t *testing.T, store any, stats *trainedStats, n *notified) *Monitor {
	t.Helper()
	if stats == nil {
		stats = &trainedStats{}
	}
	if n == nil {
		n = &notified{}
	}
	return New(testConfig, Deps{Logs: func() any { return store }, Stats: stats.get, Notify: n.notify})
}

// drain processes what Observe queued, as Run's loop does.
func drain(t *testing.T, m *Monitor) {
	t.Helper()
	for len(m.queue) > 0 {
		m.absorb(t.Context(), <-m.queue)
	}
}

func trainedModel(vhost, name string, mon storage.MLMonitoring) storage.MLModel {
	return storage.MLModel{VHost: vhost, Name: name, Backend: storage.MLBackendWorker, RemoteVersion: "4",
		Features: []string{"age", "plan"}, Monitoring: mon}
}

func observation(m storage.MLModel, inputs ...map[string]any) Observation {
	outputs := make([]map[string]any, len(inputs))
	for i := range inputs {
		outputs[i] = map[string]any{"label": "yes", "probability": 0.9}
	}
	return Observation{Model: m, CallerKind: storage.MLCallerWorkflow, CallerID: "wf-7",
		Inputs: inputs, Outputs: outputs, Latency: 25 * time.Millisecond, At: time.Now()}
}

func TestObserveNeverBlocksAndCountsWhatItDrops(t *testing.T) {
	m := New(Config{Queue: 1}, Deps{Logs: func() any { return &logStore{} }})
	model := trainedModel("drop-v", "drop-m", storage.MLMonitoring{LogSampleRate: 1})
	before := testutil.ToFloat64(droppedTotal.WithLabelValues("drop-v", "drop-m", reasonQueueFull))

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 3 {
			m.Observe(observation(model, map[string]any{"age": 30.0}))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Observe blocked on a full queue")
	}
	if got := testutil.ToFloat64(droppedTotal.WithLabelValues("drop-v", "drop-m", reasonQueueFull)) - before; got != 2 {
		t.Errorf("dropped = %v, want 2 of 3 with room for one", got)
	}

	var none *Monitor
	none.Observe(observation(model)) // monitoring off: a no-op, not a panic
}

func TestNothingIsQueuedForAModelWithNothingToWatch(t *testing.T) {
	m := newTestMonitor(t, &logStore{}, nil, nil)
	external := storage.MLModel{VHost: "v", Name: "fraud", Backend: "oip", URL: "http://ml"}
	m.Observe(observation(external, map[string]any{"amount": 1.0}))
	if len(m.queue) != 0 {
		t.Error("a model with logging off and no training stats was queued")
	}
}

func TestPredictionsAreLoggedMaskedOnACopy(t *testing.T) {
	store := &logStore{}
	m := newTestMonitor(t, store, nil, nil)
	model := trainedModel("v", "churn", storage.MLMonitoring{LogSampleRate: 1, LogMaskFields: []string{"email"}, LogMaskType: "email"})
	input := map[string]any{"age": 30.0, "plan": "pro", "email": "jane@example.com"}

	m.Observe(observation(model, input))
	input["age"] = 99.0 // the caller reuses its map after Predict returns
	drain(t, m)
	m.flush(t.Context())

	rows := store.logged()
	if len(rows) != 1 {
		t.Fatalf("logged %d rows, want 1", len(rows))
	}
	r := rows[0]
	if r.VHost != "v" || r.Model != "churn" || r.Version != "4" || r.CallerKind != storage.MLCallerWorkflow ||
		r.CallerID != "wf-7" || r.LatencyMs != 25 || r.Timestamp.IsZero() {
		t.Errorf("row = %+v", r)
	}
	if r.Inputs["email"] != "j****@example.com" || r.Inputs["age"] != 30.0 || r.Outputs["label"] != "yes" {
		t.Errorf("inputs = %v, outputs = %v", r.Inputs, r.Outputs)
	}
	if input["email"] != "jane@example.com" {
		t.Errorf("masking reached the caller's row: %v", input["email"])
	}
}

func TestOnlyTheSampledShareIsLogged(t *testing.T) {
	store := &logStore{}
	m := New(Config{Queue: 4096, Batch: 100000}, Deps{Logs: func() any { return store }})
	model := storage.MLModel{VHost: "v", Name: "fraud", Backend: "oip", Monitoring: storage.MLMonitoring{LogSampleRate: 0.25}}
	rows := make([]map[string]any, 1000)
	for i := range rows {
		rows[i] = map[string]any{"n": float64(i)}
	}
	for range 4 {
		m.Observe(observation(model, rows...))
	}
	drain(t, m)
	m.flush(t.Context())
	if got := len(store.logged()); got < 800 || got > 1200 {
		t.Errorf("logged %d of 4000 at a 0.25 sample rate", got)
	}
}

func TestLogsThatCannotBeWrittenAreDroppedAndCounted(t *testing.T) {
	for _, tt := range []struct {
		name   string
		store  any
		reason string
	}{
		{"no prediction log store", struct{}{}, reasonUnsupported},
		{"a failing write", &logStore{err: errors.New("disk full")}, reasonWriteFailed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			vhost := "fail-" + tt.reason
			m := newTestMonitor(t, tt.store, nil, nil)
			before := testutil.ToFloat64(logsDroppedTotal.WithLabelValues(vhost, "m", tt.reason))
			model := storage.MLModel{VHost: vhost, Name: "m", Backend: "oip", Monitoring: storage.MLMonitoring{LogSampleRate: 1}}
			m.Observe(observation(model, map[string]any{"n": 1.0}, map[string]any{"n": 2.0}))
			drain(t, m)
			m.flush(t.Context())
			if got := testutil.ToFloat64(logsDroppedTotal.WithLabelValues(vhost, "m", tt.reason)) - before; got != 2 {
				t.Errorf("dropped = %v, want 2", got)
			}
		})
	}
}

// rowsLike returns n rows whose age and plan follow the training split.
func rowsLike(n int) []map[string]any {
	ages := []any{5.0, 15.0, 15.0, 25.0, nil} // 0.2 | 0.4 | 0.2 | missing 0.2
	plans := []any{"pro", "pro", "pro", "pro", "pro", "pro", "3", "3", "3", "basic"}
	out := make([]map[string]any, n)
	for i := range out {
		out[i] = map[string]any{"age": ages[i%len(ages)], "plan": plans[i%len(plans)]}
	}
	return out
}

func skewedRows(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := range out {
		out[i] = map[string]any{"age": 99.0, "plan": "pro"}
	}
	return out
}

func TestDriftIsMeasuredAgainstTheTrainingStatsAndAlertsOncePerWindow(t *testing.T) {
	stats := &trainedStats{stats: map[string]worker.FeatureStats{"age": ageStats, "plan": planStats}}
	n := &notified{}
	m := newTestMonitor(t, &logStore{}, stats, n)
	var hooked []Report
	m.OnDrift(func(r Report) { hooked = append(hooked, r) })
	model := trainedModel("drift-v", "churn", storage.MLMonitoring{})

	m.Observe(observation(model, rowsLike(100)...))
	drain(t, m)
	m.evaluate(t.Context(), time.Now())

	r, ok := m.Report("drift-v", "churn")
	if !ok {
		t.Fatal("no report after a full window")
	}
	if r.Status != StatusOK || r.Rows != 100 || r.Version != "4" || r.Warn != 0.1 || r.Alert != 0.25 || len(r.Features) != 2 {
		t.Fatalf("report = %+v", r)
	}
	for _, fd := range r.Features {
		if fd.PSI > 0.05 {
			t.Errorf("%s drifted %v on rows like its training", fd.Feature, fd.PSI)
		}
	}
	if n.count() != 0 {
		t.Error("a window without drift sent an alert")
	}

	m.Observe(observation(model, skewedRows(150)...))
	drain(t, m)
	m.evaluate(t.Context(), time.Now())
	r, _ = m.Report("drift-v", "churn")
	if r.Status != StatusAlert || r.Features[0].Feature != "age" || r.Features[0].Status != StatusAlert || r.Rows != 150 {
		t.Fatalf("report = %+v", r)
	}
	if got := testutil.ToFloat64(featureDrift.WithLabelValues("drift-v", "churn", "age")); got != r.Features[0].PSI {
		t.Errorf("hermod_ml_feature_drift = %v, want %v", got, r.Features[0].PSI)
	}
	if n.count() != 1 {
		t.Errorf("alerts = %d, want 1", n.count())
	}
	// The window closed with the report; nothing new arrived, so no new one.
	m.evaluate(t.Context(), time.Now())
	if n.count() != 1 || len(hooked) != 2 {
		t.Errorf("alerts = %d, hooks = %d; want 1 and 2", n.count(), len(hooked))
	}
	if stats.asked != 1 {
		t.Errorf("training stats were read %d times for one version", stats.asked)
	}
}

func TestThresholdsComeFromTheModel(t *testing.T) {
	stats := &trainedStats{stats: map[string]worker.FeatureStats{"age": ageStats}}
	m := newTestMonitor(t, &logStore{}, stats, nil)
	model := trainedModel("v", "churn", storage.MLMonitoring{DriftWarn: 10, DriftAlert: 50})
	m.Observe(observation(model, skewedRows(100)...))
	drain(t, m)
	m.evaluate(t.Context(), time.Now())
	if r, _ := m.Report("v", "churn"); r.Status != StatusOK || r.Warn != 10 || r.Alert != 50 {
		t.Errorf("report = %+v", r)
	}
}

func TestAWindowWaitsForEnoughRows(t *testing.T) {
	stats := &trainedStats{stats: map[string]worker.FeatureStats{"age": ageStats}}
	m := newTestMonitor(t, &logStore{}, stats, nil)
	model := trainedModel("v", "churn", storage.MLMonitoring{})
	m.Observe(observation(model, rowsLike(60)...))
	drain(t, m)
	m.evaluate(t.Context(), time.Now())
	if _, ok := m.Report("v", "churn"); ok {
		t.Fatal("a report from 60 rows with 100 needed")
	}
	m.Observe(observation(model, rowsLike(60)...))
	drain(t, m)
	m.evaluate(t.Context(), time.Now())
	if r, ok := m.Report("v", "churn"); !ok || r.Rows != 120 {
		t.Errorf("report = %+v, %v; want the 120 rows of one longer window", r, ok)
	}
}

func TestANewLiveVersionStartsAFreshWindow(t *testing.T) {
	stats := &trainedStats{stats: map[string]worker.FeatureStats{"age": ageStats}}
	m := newTestMonitor(t, &logStore{}, stats, nil)
	v4 := trainedModel("v", "churn", storage.MLMonitoring{})
	m.Observe(observation(v4, skewedRows(80)...))
	drain(t, m)
	v5 := v4
	v5.RemoteVersion = "5"
	m.Observe(observation(v5, rowsLike(100)...))
	drain(t, m)
	m.evaluate(t.Context(), time.Now())
	r, ok := m.Report("v", "churn")
	if !ok || r.Version != "5" || r.Rows != 100 || r.Status != StatusOK {
		t.Errorf("report = %+v; version 4's rows leaked into version 5's window", r)
	}
	if stats.asked != 2 {
		t.Errorf("stats asked %d times, want once per version", stats.asked)
	}
}

func TestAVersionWithoutTrainingStatsHasNoDriftAndIsNotAskedAgain(t *testing.T) {
	stats := &trainedStats{}
	m := newTestMonitor(t, &logStore{}, stats, nil)
	model := trainedModel("v", "churn", storage.MLMonitoring{})
	for range 3 {
		m.Observe(observation(model, rowsLike(100)...))
		drain(t, m)
		m.evaluate(t.Context(), time.Now())
	}
	if _, ok := m.Report("v", "churn"); ok {
		t.Error("a report without training stats")
	}
	if stats.asked != 1 {
		t.Errorf("asked %d times for stats a version does not have", stats.asked)
	}
}

func TestAnUnreachableWorkerIsAskedAgainLater(t *testing.T) {
	stats := &trainedStats{err: errors.New("connection refused")}
	m := newTestMonitor(t, &logStore{}, stats, nil)
	now := time.Now()
	m.now = func() time.Time { return now }
	model := trainedModel("v", "churn", storage.MLMonitoring{})

	m.Observe(observation(model, rowsLike(10)...))
	m.Observe(observation(model, rowsLike(10)...))
	drain(t, m)
	if stats.asked != 1 {
		t.Fatalf("asked %d times within the retry delay", stats.asked)
	}
	stats.mu.Lock()
	stats.err, stats.stats = nil, map[string]worker.FeatureStats{"age": ageStats}
	stats.mu.Unlock()
	now = now.Add(statsRetry + time.Second)
	m.Observe(observation(model, rowsLike(100)...))
	drain(t, m)
	m.evaluate(t.Context(), now)
	if r, ok := m.Report("v", "churn"); !ok || r.Rows != 100 {
		t.Errorf("report = %+v, %v after the worker came back", r, ok)
	}
}

func TestForgetDropsAModelsReportAndWindow(t *testing.T) {
	stats := &trainedStats{stats: map[string]worker.FeatureStats{"age": ageStats}}
	m := newTestMonitor(t, &logStore{}, stats, nil)
	model := trainedModel("v", "gone", storage.MLMonitoring{})
	m.Observe(observation(model, rowsLike(100)...))
	drain(t, m)
	m.evaluate(t.Context(), time.Now())
	m.Observe(observation(model, rowsLike(100)...))
	drain(t, m)

	m.Forget("v", "gone")
	m.evaluate(t.Context(), time.Now())
	if _, ok := m.Report("v", "gone"); ok {
		t.Error("a deleted model still has a drift report")
	}
}

// Run is the loop: it takes what Observe queues, writes logs on its flush
// tick, and writes what is pending when it stops.
func TestRunWritesLogsAndFlushesOnStop(t *testing.T) {
	store := &logStore{}
	m := New(Config{Window: time.Hour, Flush: time.Hour}, Deps{Logs: func() any { return store }})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.Run(ctx)
	}()
	model := storage.MLModel{VHost: "v", Name: "fraud", Backend: "oip", Monitoring: storage.MLMonitoring{LogSampleRate: 1}}
	for i := range 3 {
		m.Observe(observation(model, map[string]any{"n": float64(i)}))
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(m.queue) > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	if got := len(store.logged()); got != 3 {
		t.Errorf("logged %d rows on stop, want 3", got)
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("HERMOD_ML_DRIFT_WINDOW", "90s")
	t.Setenv("HERMOD_ML_DRIFT_MIN_ROWS", "250")
	c := ConfigFromEnv()
	if c.Window != 90*time.Second || c.MinRows != 250 {
		t.Errorf("config = %+v", c)
	}
	t.Setenv("HERMOD_ML_DRIFT_WINDOW", "soon")
	t.Setenv("HERMOD_ML_DRIFT_MIN_ROWS", "-3")
	if c := ConfigFromEnv().withDefaults(); c.Window != DefaultWindow || c.MinRows != DefaultMinRows {
		t.Errorf("bad values were not replaced by the defaults: %+v", c)
	}
}
