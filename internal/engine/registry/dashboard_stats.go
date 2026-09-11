package registry

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

const (
	// defaultDashboardSampleInterval is how often the dashboard is recomputed,
	// pushed to watchers and appended to history.
	//
	// Five seconds matches runStatusFlusher, which is what writes the counters
	// GetDashboardStats reads back out of the database. Sampling faster than
	// the flusher would just re-read the same row and record a flat line at a
	// higher resolution.
	defaultDashboardSampleInterval = 5 * time.Second

	// defaultDashboardHistoryRetention bounds the table. A row every five
	// seconds is roughly 17k rows per day per watched vhost — measured in
	// SQLite, a week of one series is about 11 MB of table and index — so
	// without a sweep this becomes the largest table in the metadata database
	// within a month.
	defaultDashboardHistoryRetention = 7 * 24 * time.Hour
)

// dashboardSampleInterval and dashboardHistoryRetention are what this feature
// costs the host, and both are read from the environment because the honest
// answer to "how much disk should the chart use?" depends on the box. A week
// at five-second resolution is a fair trade on a server and a bad one on the
// small self-hosted machines Hermod is built for, and until these were
// configurable the only way to spend less was to fork.
//
// The two multiply: halving the resolution and halving the window is a
// quarter of the rows. Malformed values fall back to the default rather than
// to zero — a typo in an env var must not silently delete the series.

// dashboardSampleInterval is how often a sample is taken, from
// HERMOD_DASHBOARD_SAMPLE_INTERVAL (a Go duration, e.g. "30s").
func dashboardSampleInterval() time.Duration {
	if d, ok := durationFromEnv("HERMOD_DASHBOARD_SAMPLE_INTERVAL"); ok && d > 0 {
		return d
	}
	return defaultDashboardSampleInterval
}

// dashboardHistoryRetention is how long samples are kept, from
// HERMOD_DASHBOARD_HISTORY_RETENTION (a Go duration, e.g. "6h").
//
// Zero is meaningful and is the escape hatch for a host that wants the live
// dashboard and none of the disk: nothing is recorded, and the hourly sweep
// clears anything already there. The chart then shows only what the open page
// has collected, which is exactly where it was before history existed.
func dashboardHistoryRetention() time.Duration {
	if d, ok := durationFromEnv("HERMOD_DASHBOARD_HISTORY_RETENTION"); ok && d >= 0 {
		return d
	}
	return defaultDashboardHistoryRetention
}

func durationFromEnv(key string) (time.Duration, bool) {
	v := os.Getenv(key)
	if v == "" {
		return 0, false
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, false
	}
	return d, true
}

// engineTelemetry is the part of the dashboard that comes from engines running
// on this node rather than from the database.
//
// Each field is already computed per workflow inside telemetry.StatusUpdate and
// simply had nowhere to go: the dashboard read Throughput and lag and dropped
// the rest on the floor.
type engineTelemetry struct {
	Throughput          float64
	Lag                 uint64
	AvgLatencyMs        float64
	Backpressure        float64
	CircuitBreakersOpen int
}

// aggregateEngineTelemetry folds per-engine readings into one dashboard row.
//
// The three combining rules are deliberately different, because the quantities
// are: throughput adds, latency averages, and backpressure takes the worst
// case. Averaging backpressure would report one jammed sink among nine idle
// ones as 10% full, which is the reading least likely to get anyone to look.
func aggregateEngineTelemetry(updates []telemetry.StatusUpdate) engineTelemetry {
	var agg engineTelemetry
	var latencySum float64
	var latencyCount int

	for _, u := range updates {
		agg.Throughput += u.Throughput

		if lag, ok := u.NodeMetrics["source_lag"]; ok {
			agg.Lag += lag
		} else if lag, ok := u.NodeMetrics["lag"]; ok {
			agg.Lag += lag
		}

		// An engine reporting no latency is idle, not instantaneous. Counting
		// its zero would halve the figure for every quiet workflow somebody
		// left enabled.
		if u.AvgLatency > 0 {
			latencySum += float64(u.AvgLatency) / float64(time.Millisecond)
			latencyCount++
		}

		for _, fill := range u.SinkBufferFill {
			if fill > agg.Backpressure {
				agg.Backpressure = fill
			}
		}

		for _, status := range u.SinkCBStatuses {
			// "half-open" is a breaker probing recovery, not a broken sink.
			if status == "open" {
				agg.CircuitBreakersOpen++
			}
		}
	}

	if latencyCount > 0 {
		agg.AvgLatencyMs = latencySum / float64(latencyCount)
	}
	return agg
}

// deriveErrorRate is the dead-lettered share of everything attempted.
//
// total_errors is exactly the dead-letter count (flushStatsToStorage persists
// baseErrors + StatusUpdate.DeadLetterCount), and total_processed counts what
// got through, so the denominator is the sum rather than either one alone.
// Zero traffic reports 0 rather than NaN: the card renders this as a
// percentage, and "NaN%" on a fresh install reads as a broken dashboard.
func deriveErrorRate(processed, errs uint64) float64 {
	total := processed + errs
	if total == 0 {
		return 0
	}
	return float64(errs) / float64(total)
}

// engineStatuses snapshots the status of every engine matching vhost.
//
// The statuses are copied out under the lock and folded outside it: GetStatus
// walks each engine's sink writers and takes their mutexes, which is not work
// to do while holding the registry-wide lock every request path needs.
func (r *Registry) engineStatuses(vhost string) []telemetry.StatusUpdate {
	r.mu.RLock()
	defer r.mu.RUnlock()

	updates := make([]telemetry.StatusUpdate, 0, len(r.engines))
	for _, ae := range r.engines {
		if vhost != "" && vhost != "all" && ae.workflow.VHost != vhost {
			continue
		}
		updates = append(updates, ae.engine.GetStatus())
	}
	return updates
}

// broadcastDashboardStats delivers stats to everyone subscribed under key.
//
// Sends are non-blocking, matching every other broadcast here: a browser tab
// that has stopped reading must not be able to stall the sampler for every
// other watcher. A dropped frame is invisible — the next tick carries the
// current numbers anyway, since these are levels rather than deltas.
func (r *Registry) broadcastDashboardStats(key string, stats storage.DashboardStats) {
	r.statusSubsMu.RLock()
	defer r.statusSubsMu.RUnlock()

	for ch := range r.dashboardSubs[key] {
		select {
		case ch <- stats:
		default:
		}
	}
}

// runDashboardSampler keeps the dashboard moving and records its history.
//
// Before this, the only thing that ever pushed dashboard stats was
// BroadcastStatus, which is wired to an engine's SetOnStatusChange. With no
// workflow running there is no engine, so nothing fired: the WebSocket
// delivered one snapshot on connect and then went silent, and uptime, worker
// count and health sat frozen on screen at whatever they were when the page
// loaded. Measured against a running server, that was one message in fifteen
// seconds.
//
// So the tick is the floor rather than the only source: BroadcastStatus still
// pushes on engine activity (throttled to 500ms) so a busy pipeline stays
// responsive, and this guarantees an update even when nothing is happening —
// which is precisely when a stalled dashboard is most misleading.
func (r *Registry) runDashboardSampler(interval time.Duration) {
	defer func() {
		if p := recover(); p != nil {
			r.logger.Error("Registry: dashboard sampler panicked", "panic", p)
		}
	}()

	if interval <= 0 {
		interval = defaultDashboardSampleInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.sampleDashboard()
		}
	}
}

// sampleDashboard recomputes each watched dashboard, pushes it and stores it.
func (r *Registry) sampleDashboard() {
	// Bounded so a slow or wedged metadata database cannot pile ticks up on
	// top of each other.
	ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
	defer cancel()

	r.statusSubsMu.RLock()
	subKeys := make([]string, 0, len(r.dashboardSubs))
	for key := range r.dashboardSubs {
		subKeys = append(subKeys, key)
	}
	r.statusSubsMu.RUnlock()

	// Group the subscriber keys by the series they actually name, so "" and
	// "all" are computed once rather than twice. The global aggregate is always
	// present: history exists to answer questions asked after the fact, and a
	// series that only accrues while someone has the page open is missing for
	// exactly the outage nobody was watching.
	groups := map[string][]string{"": nil}
	for _, key := range subKeys {
		normalized := storage.NormalizeVHost(key)
		groups[normalized] = append(groups[normalized], key)
	}

	store := r.GetStorage()

	// Decided once per tick rather than per series: retention is read from the
	// environment, and a host that set it to zero wants the live broadcast
	// below and no disk at all.
	recordHistory := store != nil &&
		dashboardHistoryRetention() > 0 &&
		!r.historyUnsupported.Load()

	for vhost, keys := range groups {
		stats, err := r.GetDashboardStats(ctx, vhost)
		if err != nil {
			r.logger.Error("Registry: dashboard sample failed", "vhost", vhost, "error", err)
			continue
		}

		for _, key := range keys {
			r.broadcastDashboardStats(key, stats)
		}

		if recordHistory {
			recordHistory = r.recordSample(ctx, store, vhost, stats)
		}
	}
}

// recordSample persists one point of a series and reports whether history is
// worth recording at all on the remaining ticks.
//
// A backend that cannot store history will never be able to, so a false return
// latches for the life of the registry rather than being retried five seconds
// later, forever.
func (r *Registry) recordSample(
	ctx context.Context,
	store storage.Storage,
	vhost string,
	stats storage.DashboardStats,
) bool {
	err := store.RecordDashboardSample(ctx, storage.DashboardSample{
		Timestamp:       time.Now().UTC(),
		VHost:           vhost,
		Throughput:      stats.Throughput,
		TotalProcessed:  stats.TotalProcessed,
		TotalErrors:     stats.TotalErrors,
		TotalLag:        stats.TotalLag,
		ErrorRate:       stats.ErrorRate,
		AvgLatencyMs:    stats.AvgLatencyMs,
		ActiveWorkflows: stats.ActiveWorkflows,
		ActiveWorkers:   stats.ActiveWorkers,
	})
	switch {
	case err == nil:
		return true
	case errors.Is(err, hermod.ErrNotSupported):
		// Logged once, at info: it is a property of the chosen backend, not a
		// fault, and at a five-second tick an error would be 17k lines a day.
		r.historyUnsupported.Store(true)
		r.logger.Info("Registry: backend does not store dashboard history; "+
			"the chart will show only the open page's readings", "error", err)
		return false
	default:
		r.logger.Error("Registry: recording dashboard history failed", "vhost", vhost, "error", err)
		return true
	}
}

// GetDashboardHistory reads back the persisted series for a vhost.
func (r *Registry) GetDashboardHistory(ctx context.Context, vhost string, since time.Time, limit int) ([]storage.DashboardSample, error) {
	store := r.GetStorage()
	if store == nil {
		return nil, nil
	}
	return store.GetDashboardHistory(ctx, vhost, since, limit)
}
