// Package monitor watches what a vhost's models are asked and answer. It
// keeps a sampled, masked log of predictions in the log store, and measures
// drift: how far each feature's live values have moved from the rows the
// model version was trained on (the worker's feature_stats), as a population
// stability index per feature.
//
// Nothing here may slow or fail a prediction. Observe copies what it needs and
// hands it to a bounded queue without blocking; a full queue drops the
// observation and counts it. Masking, counting, writing and alerting all run
// on the one goroutine Run starts.
//
// Drift is counted in memory, per Hermod process, over every prediction that
// process makes (not only the logged sample), in tumbling windows: at each
// tick of Config.Window a model's window is judged if it holds at least
// Config.MinRows rows, and a new one starts. Several Hermod processes each
// judge their own traffic; their hermod_ml_feature_drift series carry the
// scrape's instance label, and the drift API answers for the process that
// serves it. Merging counts across processes through storage was left out:
// the default deployment runs the API and the workflow engine in one process,
// where this is all the traffic there is.
package monitor

import (
	"cmp"
	"context"
	"encoding/json"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/transformer/security"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// Defaults of Config.
const (
	DefaultWindow  = 5 * time.Minute
	DefaultMinRows = 100

	defaultQueue = 1024
	defaultBatch = 200
	defaultFlush = time.Second
)

// statsRetry is how long a version's training stats are not asked for again
// after the worker failed to answer; ioTimeout bounds one write or one ask.
const (
	statsRetry   = 30 * time.Second
	ioTimeout    = 10 * time.Second
	errorLogGap  = time.Minute
	defaultMaskT = "all"
)

// Config sizes the monitor. Zero values are the defaults.
type Config struct {
	// Window is how often drift is judged; MinRows how many predicted rows a
	// window needs before it is.
	Window  time.Duration
	MinRows int
	// Queue bounds the observations waiting for the monitor's goroutine.
	Queue int
	// Batch and Flush bound how many logged rows wait to be written, and for
	// how long.
	Batch int
	Flush time.Duration
}

// ConfigFromEnv reads HERMOD_ML_DRIFT_WINDOW (a Go duration) and
// HERMOD_ML_DRIFT_MIN_ROWS. A value that does not read is left to the default.
func ConfigFromEnv() Config {
	var c Config
	if d, err := time.ParseDuration(os.Getenv("HERMOD_ML_DRIFT_WINDOW")); err == nil {
		c.Window = d
	}
	if n, err := strconv.Atoi(os.Getenv("HERMOD_ML_DRIFT_MIN_ROWS")); err == nil {
		c.MinRows = n
	}
	return c
}

func (c Config) withDefaults() Config {
	if c.Window <= 0 {
		c.Window = DefaultWindow
	}
	if c.MinRows <= 0 {
		c.MinRows = DefaultMinRows
	}
	if c.Queue <= 0 {
		c.Queue = defaultQueue
	}
	if c.Batch <= 0 {
		c.Batch = defaultBatch
	}
	if c.Flush <= 0 {
		c.Flush = defaultFlush
	}
	return c
}

// StatsFunc answers the training stats of one model version: none, without
// an error, for a version trained before the worker recorded them.
type StatsFunc func(ctx context.Context, vhost, model, version string) (map[string]worker.FeatureStats, error)

// NotifyFunc raises an alert for a report whose status is StatusAlert.
type NotifyFunc func(ctx context.Context, r Report)

// Deps are what the monitor reads from and reports to.
type Deps struct {
	// Logs returns the current log store; one that is not a
	// storage.MLPredictionLogStore drops logged rows, counted.
	Logs   func() any
	Stats  StatsFunc
	Notify NotifyFunc
	Logger hermod.Logger
}

// Observation is one prediction call as the caller made it.
type Observation struct {
	// Model is the model as it was called: its live version, features and
	// monitoring settings.
	Model      storage.MLModel
	CallerKind string
	CallerID   string
	// Inputs and Outputs are the rows sent and the predictions returned, in
	// the same order.
	Inputs  []map[string]any
	Outputs []map[string]any
	Latency time.Duration
	At      time.Time
}

type modelKey struct{ vhost, model string }

// event is an observation reduced to what the monitor's goroutine needs,
// and copied, so the caller is free to change its rows once Observe returns.
type event struct {
	key      modelKey
	version  string
	mon      storage.MLMonitoring
	features []string
	values   [][]any // per row, aligned with features; nil when drift is not measured
	logs     []storage.MLPredictionLog
}

// window counts one model version's live values until it is judged.
type window struct {
	version     string
	start       time.Time
	rows        int64
	warn, alert float64
	hists       map[string]*histogram // empty: the version has no training stats
}

// versionStats are the training stats of the version a model's window counts.
type versionStats struct {
	version string
	stats   map[string]worker.FeatureStats
	known   bool
	retryAt time.Time
}

// Monitor is safe for concurrent use. The zero value is not usable; a nil
// *Monitor ignores every observation.
type Monitor struct {
	cfg   Config
	deps  Deps
	queue chan event
	now   func() time.Time

	mu      sync.Mutex
	windows map[modelKey]*window
	stats   map[modelKey]versionStats
	reports map[modelKey]Report
	hooks   []func(Report)

	// Run's goroutine only.
	pending    []storage.MLPredictionLog
	lastErrLog time.Time
}

// New builds a monitor. Run must be started for anything to be logged or
// judged.
func New(cfg Config, deps Deps) *Monitor {
	cfg = cfg.withDefaults()
	return &Monitor{
		cfg: cfg, deps: deps, queue: make(chan event, cfg.Queue), now: time.Now,
		windows: map[modelKey]*window{}, stats: map[modelKey]versionStats{}, reports: map[modelKey]Report{},
	}
}

// Window is how often drift is judged.
func (m *Monitor) Window() time.Duration { return m.cfg.Window }

// MinRows is how many rows a window needs to be judged.
func (m *Monitor) MinRows() int { return m.cfg.MinRows }

// OnDrift adds fn to what is called with every drift report, whatever its
// status, once per judged window. It is the hook a retraining policy uses;
// fn runs on the monitor's goroutine and must not block.
func (m *Monitor) OnDrift(fn func(Report)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooks = append(m.hooks, fn)
}

// Report returns the latest drift report of a model.
func (m *Monitor) Report(vhost, model string) (Report, bool) {
	if m == nil {
		return Report{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.reports[modelKey{vhost, model}]
	return r, ok
}

// Forget drops what is held about a model: it was deleted.
func (m *Monitor) Forget(vhost, model string) {
	if m == nil {
		return
	}
	k := modelKey{vhost, model}
	m.mu.Lock()
	delete(m.windows, k)
	delete(m.stats, k)
	delete(m.reports, k)
	m.mu.Unlock()
	featureDrift.DeletePartialMatch(prometheus.Labels{"vhost": vhost, "model": model})
}

// Observe hands one prediction call to the monitor. It never blocks: when the
// queue is full the call is dropped and counted.
func (m *Monitor) Observe(o Observation) {
	if m == nil {
		return
	}
	mdl := o.Model
	ev := event{key: modelKey{mdl.VHost, mdl.Name}, version: mdl.RemoteVersion, mon: mdl.Monitoring}

	// Only a model Hermod trained has training stats to measure drift against.
	if mdl.Backend == storage.MLBackendWorker && ev.version != "" && len(mdl.Features) > 0 {
		ev.features = mdl.Features
		ev.values = featureValues(o.Inputs, ev.features)
	}
	ev.logs = sample(o, ev.version)

	if ev.values == nil && len(ev.logs) == 0 {
		return
	}
	select {
	case m.queue <- ev:
	default:
		droppedTotal.WithLabelValues(mdl.VHost, mdl.Name, reasonQueueFull).Inc()
	}
}

// featureValues copies each row's features, in order, out of the caller's rows.
func featureValues(rows []map[string]any, features []string) [][]any {
	values := make([][]any, len(rows))
	for i, row := range rows {
		vals := make([]any, len(features))
		for j, f := range features {
			vals[j] = detach(row[f])
		}
		values[i] = vals
	}
	return values
}

// sample picks the rows the model's sample rate logs, copied, unmasked yet.
func sample(o Observation, version string) []storage.MLPredictionLog {
	mdl := o.Model
	rate := mdl.Monitoring.LogSampleRate
	if rate <= 0 {
		return nil
	}
	at := o.At
	if at.IsZero() {
		at = time.Now()
	}
	latency := float64(o.Latency.Microseconds()) / 1000
	var logs []storage.MLPredictionLog
	for i, row := range o.Inputs {
		if rand.Float64() >= rate { //nolint:gosec // G404: a sampling decision; nothing relies on it being unpredictable.
			continue
		}
		var out map[string]any
		if i < len(o.Outputs) {
			out = copyMap(o.Outputs[i])
		}
		logs = append(logs, storage.MLPredictionLog{
			VHost: mdl.VHost, Model: mdl.Name, Version: version, Timestamp: at,
			Inputs: copyMap(row), Outputs: out, LatencyMs: latency,
			CallerKind: o.CallerKind, CallerID: o.CallerID,
		})
	}
	return logs
}

// Run processes observations until ctx ends, then writes what is pending.
func (m *Monitor) Run(ctx context.Context) {
	defer func() {
		if p := recover(); p != nil {
			m.log("ML monitor stopped on a panic; predictions are no longer logged or judged for drift", "panic", p)
		}
	}()
	judge := time.NewTicker(m.cfg.Window)
	defer judge.Stop()
	flush := time.NewTicker(m.cfg.Flush)
	defer flush.Stop()
	for {
		select {
		case ev := <-m.queue:
			m.absorb(ctx, ev)
		case <-flush.C:
			m.flush(ctx)
		case now := <-judge.C:
			m.evaluate(ctx, now)
		case <-ctx.Done():
			for {
				select {
				case ev := <-m.queue:
					m.absorb(ctx, ev)
				default:
					m.flush(ctx)
					return
				}
			}
		}
	}
}

func (m *Monitor) absorb(ctx context.Context, ev event) {
	if len(ev.logs) > 0 {
		maskType := ev.mon.LogMaskType
		if maskType == "" {
			maskType = defaultMaskT
		}
		if len(ev.mon.LogMaskFields) > 0 {
			for _, l := range ev.logs {
				security.MaskFields(l.Inputs, ev.mon.LogMaskFields, maskType)
				security.MaskFields(l.Outputs, ev.mon.LogMaskFields, maskType)
			}
		}
		m.pending = append(m.pending, ev.logs...)
		if len(m.pending) >= m.cfg.Batch {
			m.flush(ctx)
		}
	}
	if ev.values != nil {
		m.count(ctx, ev)
	}
}

// count adds an event's values to its model's window, starting a new window
// when the live version changed.
func (m *Monitor) count(ctx context.Context, ev event) {
	m.mu.Lock()
	w := m.windows[ev.key]
	if w == nil || w.version != ev.version {
		m.mu.Unlock()
		hists, ok := m.histograms(ctx, ev)
		if !ok {
			return // the worker did not answer; these rows go uncounted
		}
		m.mu.Lock()
		w = &window{version: ev.version, start: m.now(), hists: hists}
		m.windows[ev.key] = w
	}
	defer m.mu.Unlock()
	w.warn, w.alert = ev.mon.Thresholds()
	w.rows += int64(len(ev.values))
	if len(w.hists) == 0 {
		return
	}
	for _, vals := range ev.values {
		for j, f := range ev.features {
			if h := w.hists[f]; h != nil {
				h.add(vals[j])
			}
		}
	}
}

// histograms returns empty histograms for the event's version, asking for its
// training stats once. It reports false while they cannot be had.
func (m *Monitor) histograms(ctx context.Context, ev event) (map[string]*histogram, bool) {
	m.mu.Lock()
	vs, ok := m.stats[ev.key]
	m.mu.Unlock()
	if !ok || vs.version != ev.version || !vs.known {
		if ok && vs.version == ev.version && m.now().Before(vs.retryAt) {
			return nil, false
		}
		if m.deps.Stats == nil {
			return nil, true
		}
		ask, cancel := context.WithTimeout(ctx, ioTimeout)
		stats, err := m.deps.Stats(ask, ev.key.vhost, ev.key.model, ev.version)
		cancel()
		if err != nil {
			m.mu.Lock()
			m.stats[ev.key] = versionStats{version: ev.version, retryAt: m.now().Add(statsRetry)}
			m.mu.Unlock()
			m.log("ML monitor could not read a model version's training stats; its drift is not counted until it can",
				"vhost", ev.key.vhost, "model", ev.key.model, "version", ev.version, "error", err)
			return nil, false
		}
		vs = versionStats{version: ev.version, stats: stats, known: true}
		m.mu.Lock()
		m.stats[ev.key] = vs
		m.mu.Unlock()
	}
	hists := make(map[string]*histogram, len(vs.stats))
	for _, f := range ev.features {
		if st, ok := vs.stats[f]; ok {
			hists[f] = newHistogram(st)
		}
	}
	return hists, true
}

// evaluate judges every window that holds enough rows, and starts a new one.
func (m *Monitor) evaluate(ctx context.Context, now time.Time) {
	m.mu.Lock()
	var judged []Report
	for k, w := range m.windows {
		if w.rows < int64(m.cfg.MinRows) {
			continue
		}
		next := &window{version: w.version, start: now, warn: w.warn, alert: w.alert, hists: map[string]*histogram{}}
		for f, h := range w.hists {
			next.hists[f] = newHistogram(h.stats)
		}
		if len(w.hists) > 0 {
			r := judge(k, w, now)
			m.reports[k] = r
			judged = append(judged, r)
		}
		m.windows[k] = next
	}
	hooks := slices.Clone(m.hooks)
	m.mu.Unlock()

	for _, r := range judged {
		featureDrift.DeletePartialMatch(prometheus.Labels{"vhost": r.VHost, "model": r.Model})
		for _, fd := range r.Features {
			featureDrift.WithLabelValues(r.VHost, r.Model, fd.Feature).Set(fd.PSI)
		}
		if r.Status == StatusAlert && m.deps.Notify != nil {
			m.deps.Notify(ctx, r)
		}
		for _, h := range hooks {
			h(r)
		}
	}
}

// judge reports one window: each feature's drift, most drifted first, and the
// worst status among them.
func judge(k modelKey, w *window, now time.Time) Report {
	r := Report{
		VHost: k.vhost, Model: k.model, Version: w.version, WindowStart: w.start, WindowEnd: now,
		Rows: w.rows, Warn: w.warn, Alert: w.alert, Status: StatusOK,
	}
	for f, h := range w.hists {
		fd := h.drift(f, w.warn, w.alert)
		r.Features = append(r.Features, fd)
		if severity(fd.Status) > severity(r.Status) {
			r.Status = fd.Status
		}
	}
	slices.SortFunc(r.Features, func(a, b FeatureDrift) int {
		if c := cmp.Compare(b.PSI, a.PSI); c != 0 {
			return c
		}
		return cmp.Compare(a.Feature, b.Feature)
	})
	return r
}

func severity(status string) int {
	switch status {
	case StatusAlert:
		return 2
	case StatusWarn:
		return 1
	}
	return 0
}

// flush writes the pending logged rows in one batch. A store that cannot
// hold them, or a write that fails, drops the batch: the log is a sample,
// and holding rows for a store that is down would grow without bound.
func (m *Monitor) flush(ctx context.Context) {
	if len(m.pending) == 0 {
		return
	}
	batch := m.pending
	m.pending = nil

	var store any
	if m.deps.Logs != nil {
		store = m.deps.Logs()
	}
	ls, ok := store.(storage.MLPredictionLogStore)
	if !ok {
		countLogs(logsDroppedTotal, batch, reasonUnsupported)
		return
	}
	// Detached: the last flush runs as Run stops, on a cancelled context.
	write, cancel := context.WithTimeout(context.WithoutCancel(ctx), ioTimeout)
	defer cancel()
	if err := ls.InsertMLPredictionLogs(write, batch); err != nil {
		countLogs(logsDroppedTotal, batch, reasonWriteFailed)
		m.log("ML monitor could not write prediction logs; the batch was dropped", "rows", len(batch), "error", err)
		return
	}
	countLogs(logsWrittenTotal, batch, "")
}

func countLogs(c *prometheus.CounterVec, batch []storage.MLPredictionLog, reason string) {
	per := map[modelKey]int{}
	for _, l := range batch {
		per[modelKey{l.VHost, l.Model}]++
	}
	for k, n := range per {
		if reason == "" {
			c.WithLabelValues(k.vhost, k.model).Add(float64(n))
		} else {
			c.WithLabelValues(k.vhost, k.model, reason).Add(float64(n))
		}
	}
}

// log reports a monitor failure, at most once a minute: a store that is down
// fails every flush.
func (m *Monitor) log(msg string, kv ...any) {
	if m.deps.Logger == nil || time.Since(m.lastErrLog) < errorLogGap {
		return
	}
	m.lastErrLog = time.Now()
	m.deps.Logger.Warn(msg, kv...)
}

// copyMap copies a row deep enough that masking it, or the caller changing
// its own, does not reach the other.
func copyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = copyValue(v)
	}
	return out
}

func copyValue(v any) any {
	switch c := v.(type) {
	case map[string]any:
		return copyMap(c)
	case []any:
		out := make([]any, len(c))
		for i, e := range c {
			out[i] = copyValue(e)
		}
		return out
	}
	return v
}

// detach makes a feature value safe to hold after Observe returns: an object
// or a list becomes its JSON text, which is what the worker's to_text makes
// of one.
func detach(v any) any {
	switch v.(type) {
	case map[string]any, []any:
		raw, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		return string(raw)
	}
	return v
}
