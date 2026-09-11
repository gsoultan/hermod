package registry

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// --- Aggregating live engine telemetry -------------------------------------
//
// The engines already compute every number below per workflow; none of it
// reached the dashboard. Folding it is kept as a pure function over status
// updates so the arithmetic can be pinned down without standing up an engine,
// a source and a sink to produce one reading.

func TestAggregateEngineTelemetry_SumsThroughputAcrossEngines(t *testing.T) {
	got := aggregateEngineTelemetry([]telemetry.StatusUpdate{
		{Throughput: 10.5},
		{Throughput: 4.5},
	})
	if got.Throughput != 15 {
		t.Errorf("Throughput = %v, want 15", got.Throughput)
	}
}

// An average, not a sum: latencies do not add up, and a summed "average
// latency" would climb with the number of workflows while every pipeline got
// faster.
func TestAggregateEngineTelemetry_AveragesLatency(t *testing.T) {
	got := aggregateEngineTelemetry([]telemetry.StatusUpdate{
		{AvgLatency: 10 * time.Millisecond},
		{AvgLatency: 20 * time.Millisecond},
	})
	if got.AvgLatencyMs != 15 {
		t.Errorf("AvgLatencyMs = %v, want 15", got.AvgLatencyMs)
	}
}

// An idle engine reports no latency at all, which is not the same as reporting
// zero milliseconds. Averaging the zeros in would halve the figure every time
// somebody left a second workflow enabled but quiet.
func TestAggregateEngineTelemetry_IdleEnginesDoNotDragLatencyDown(t *testing.T) {
	got := aggregateEngineTelemetry([]telemetry.StatusUpdate{
		{AvgLatency: 20 * time.Millisecond},
		{AvgLatency: 0},
	})
	if got.AvgLatencyMs != 20 {
		t.Errorf("AvgLatencyMs = %v, want 20 — an engine reporting no latency is "+
			"idle, not instantaneous", got.AvgLatencyMs)
	}
}

func TestAggregateEngineTelemetry_NoEnginesReportsZeroLatency(t *testing.T) {
	got := aggregateEngineTelemetry(nil)
	if got.AvgLatencyMs != 0 {
		t.Errorf("AvgLatencyMs = %v, want 0", got.AvgLatencyMs)
	}
}

// Backpressure is the worst queue in the system, not the average one. One sink
// at 100% while nine sit empty is a stalled pipeline; averaging it to 10%
// reports that as healthy.
func TestAggregateEngineTelemetry_BackpressureIsTheWorstSink(t *testing.T) {
	got := aggregateEngineTelemetry([]telemetry.StatusUpdate{
		{SinkBufferFill: map[string]float64{"a": 0.1, "b": 0.9}},
		{SinkBufferFill: map[string]float64{"c": 0.2}},
	})
	if got.Backpressure != 0.9 {
		t.Errorf("Backpressure = %v, want 0.9", got.Backpressure)
	}
}

func TestAggregateEngineTelemetry_CountsOnlyOpenCircuitBreakers(t *testing.T) {
	got := aggregateEngineTelemetry([]telemetry.StatusUpdate{
		{SinkCBStatuses: map[string]string{"a": "open", "b": "closed"}},
		{SinkCBStatuses: map[string]string{"c": "half-open", "d": "open"}},
	})
	// "half-open" is a breaker probing recovery, not a broken sink.
	if got.CircuitBreakersOpen != 2 {
		t.Errorf("CircuitBreakersOpen = %v, want 2", got.CircuitBreakersOpen)
	}
}

// The lag fallback that GetDashboardStats already relied on, pinned so it
// survives the refactor.
func TestAggregateEngineTelemetry_ReadsLagFromNodeMetrics(t *testing.T) {
	got := aggregateEngineTelemetry([]telemetry.StatusUpdate{
		{NodeMetrics: map[string]uint64{"source_lag": 7}},
		{NodeMetrics: map[string]uint64{"lag": 3}},
	})
	if got.Lag != 10 {
		t.Errorf("Lag = %v, want 10", got.Lag)
	}
}

// --- Error rate -------------------------------------------------------------

func TestDeriveErrorRate(t *testing.T) {
	tests := []struct {
		name      string
		processed uint64
		errs      uint64
		want      float64
	}{
		// Nothing has happened yet. Reporting 0% is the honest answer; a NaN
		// would render as "NaN%" on the card.
		{"no traffic", 0, 0, 0},
		{"clean run", 100, 0, 0},
		{"one in five failed", 80, 20, 0.2},
		{"everything failed", 0, 10, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveErrorRate(tc.processed, tc.errs); got != tc.want {
				t.Errorf("deriveErrorRate(%d, %d) = %v, want %v",
					tc.processed, tc.errs, got, tc.want)
			}
		})
	}
}

// --- The freeze -------------------------------------------------------------
//
// BroadcastStatus was the only thing that ever pushed dashboard stats, and it
// is wired to an engine's SetOnStatusChange. With no workflow running there is
// no engine, so nothing ever fired: the socket delivered one snapshot on
// connect and then went silent. Verified against a running server before this
// test existed — one message in fifteen seconds — which meant uptime, worker
// count and health on screen were frozen at whatever they were when the page
// loaded, with nothing to tell the reader they had stopped moving.

// recordingStorage counts the samples the registry writes.
type recordingStorage struct {
	testutil.BaseMockStorage

	mu      sync.Mutex
	samples []storage.DashboardSample
}

func (s *recordingStorage) RecordDashboardSample(ctx context.Context, sample storage.DashboardSample) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples = append(s.samples, sample)
	return nil
}

func (s *recordingStorage) recorded() []storage.DashboardSample {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storage.DashboardSample(nil), s.samples...)
}

func (s *recordingStorage) GetDashboardStats(ctx context.Context, vhost string) (storage.DashboardStats, error) {
	return storage.DashboardStats{TotalWorkflows: 1, ActiveWorkers: 2}, nil
}

func newSamplerRegistry(t *testing.T, store storage.Storage) *Registry {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	return &Registry{
		engines:       make(map[string]*activeEngine),
		storage:       store,
		dashboardSubs: make(map[string]map[chan storage.DashboardStats]bool),
		logger:        telemetry.NewDefaultLogger(),
		startTime:     time.Now(),
		ctx:           ctx,
		cancel:        cancel,
	}
}

func TestDashboardSampler_PushesUpdatesWithNoEngineActivity(t *testing.T) {
	reg := newSamplerRegistry(t, &recordingStorage{})

	ch := reg.SubscribeDashboardStats("all")
	defer reg.UnsubscribeDashboardStats(ch)

	go reg.runDashboardSampler(20 * time.Millisecond)

	// No engine is registered and nothing calls BroadcastStatus, which is
	// exactly the state the dashboard was stuck in.
	select {
	case got := <-ch:
		if got.TotalWorkflows != 1 {
			t.Errorf("broadcast carried TotalWorkflows = %d, want 1", got.TotalWorkflows)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no dashboard update arrived with no engine running; the dashboard " +
			"is still frozen at whatever it showed when the page loaded")
	}
}

func TestDashboardSampler_RecordsHistory(t *testing.T) {
	store := &recordingStorage{}
	reg := newSamplerRegistry(t, store)

	go reg.runDashboardSampler(20 * time.Millisecond)

	deadline := time.After(3 * time.Second)
	for {
		if len(store.recorded()) > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("the sampler never persisted a history point, so the chart " +
				"still cannot survive a page reload")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// Which series get written is a balance between two failures. Sampling every
// vhost that exists writes rows forever for tenants nobody looks at; sampling
// only what is on screen means history is missing for exactly the outage
// nobody was watching. So the global aggregate is always kept, and a per-tenant
// series accrues while someone has that tenant's dashboard open.
func TestDashboardSampler_KeepsGlobalAndWatchedVHostsOnly(t *testing.T) {
	store := &recordingStorage{}
	reg := newSamplerRegistry(t, store)

	var drained atomic.Int64
	ch := reg.SubscribeDashboardStats("tenant-a")
	defer reg.UnsubscribeDashboardStats(ch)
	// Drain so a full channel never masks the assertion: the sampler drops
	// sends to a full subscriber, which would look identical to not sampling.
	go func() {
		for range ch {
			drained.Add(1)
		}
	}()

	go reg.runDashboardSampler(20 * time.Millisecond)
	time.Sleep(300 * time.Millisecond)

	recorded := store.recorded()
	if len(recorded) == 0 {
		t.Fatal("the sampler recorded nothing at all")
	}

	var sawGlobal, sawTenantA bool
	for _, smp := range recorded {
		switch smp.VHost {
		case "":
			sawGlobal = true
		case "tenant-a":
			sawTenantA = true
		default:
			t.Fatalf("sampled vhost %q, which nobody is watching", smp.VHost)
		}
	}
	if !sawGlobal {
		t.Error("no global sample was recorded; history would be missing for any " +
			"period when nobody happened to have the page open")
	}
	if !sawTenantA {
		t.Error("no sample recorded for tenant-a, whose dashboard is open")
	}
}

// The sampler must not outlive the registry: Close cancels the context, and a
// ticker goroutine that ignores it leaks for the life of the process.
func TestDashboardSampler_StopsWhenRegistryCloses(t *testing.T) {
	store := &recordingStorage{}
	reg := newSamplerRegistry(t, store)

	stopped := make(chan struct{})
	go func() {
		reg.runDashboardSampler(10 * time.Millisecond)
		close(stopped)
	}()

	reg.cancel()

	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("the sampler ignored context cancellation and leaked")
	}
}

// --- What the history costs the host ----------------------------------------
//
// A sample every five seconds is roughly 17k rows per day per watched vhost.
// Measured in SQLite, a week of one series is ~11 MB of table and index. That
// is a fair trade on a server and a bad one on the small self-hosted boxes
// Hermod is meant to run on, and until now there was no way to say so: both
// the interval and the retention were constants in this file, so the only way
// to spend less disk was to fork.

// unsupportedStorage is a backend that will never store history — Pebble, or
// any future embedded backend that implements the interface without the table.
type unsupportedStorage struct {
	testutil.BaseMockStorage
	calls atomic.Int64
}

func (s *unsupportedStorage) RecordDashboardSample(context.Context, storage.DashboardSample) error {
	s.calls.Add(1)
	return fmt.Errorf("%w: pebble does not store dashboard history", hermod.ErrNotSupported)
}

func (s *unsupportedStorage) GetDashboardStats(context.Context, string) (storage.DashboardStats, error) {
	return storage.DashboardStats{TotalWorkflows: 1}, nil
}

// The sampler runs on every node, including ones whose backend cannot store a
// sample. Retrying forever costs a write attempt and a logged error every five
// seconds, which is how the backend that stores nothing ends up producing the
// most disk of any of them.
func TestDashboardSampler_StopsAskingABackendThatCannotStoreHistory(t *testing.T) {
	store := &unsupportedStorage{}
	reg := newSamplerRegistry(t, store)

	go reg.runDashboardSampler(10 * time.Millisecond)

	// Long enough for many ticks; a sampler that keeps trying will be well
	// past one call by the time this returns.
	time.Sleep(300 * time.Millisecond)

	if got := store.calls.Load(); got > 1 {
		t.Errorf("the sampler called RecordDashboardSample %d times on a backend that "+
			"reported ErrNotSupported; at the real five-second tick that is 17k "+
			"failed writes and 17k logged errors a day, forever", got)
	}
}

func TestDashboardHistoryRetention_DefaultsToSevenDays(t *testing.T) {
	if got := dashboardHistoryRetention(); got != 7*24*time.Hour {
		t.Errorf("dashboardHistoryRetention() = %v, want 168h", got)
	}
}

func TestDashboardHistoryRetention_HonoursEnv(t *testing.T) {
	t.Setenv("HERMOD_DASHBOARD_HISTORY_RETENTION", "6h")
	if got := dashboardHistoryRetention(); got != 6*time.Hour {
		t.Errorf("dashboardHistoryRetention() = %v, want 6h; a small host has no other "+
			"way to spend less disk on the chart", got)
	}
}

func TestDashboardHistoryRetention_IgnoresGarbage(t *testing.T) {
	t.Setenv("HERMOD_DASHBOARD_HISTORY_RETENTION", "7 days")
	if got := dashboardHistoryRetention(); got != 7*24*time.Hour {
		t.Errorf("dashboardHistoryRetention() = %v, want the default; a typo in an env "+
			"var must not silently turn retention off and delete the series", got)
	}
}

func TestDashboardSampleInterval_HonoursEnv(t *testing.T) {
	t.Setenv("HERMOD_DASHBOARD_SAMPLE_INTERVAL", "30s")
	if got := dashboardSampleInterval(); got != 30*time.Second {
		t.Errorf("dashboardSampleInterval() = %v, want 30s; sampling six times slower "+
			"is six times less disk", got)
	}
}

// Zero retention is the escape hatch for a host that wants the live dashboard
// and none of the disk: keep broadcasting, store nothing.
func TestDashboardSampler_ZeroRetentionStoresNothingButKeepsBroadcasting(t *testing.T) {
	t.Setenv("HERMOD_DASHBOARD_HISTORY_RETENTION", "0")

	store := &recordingStorage{}
	reg := newSamplerRegistry(t, store)

	ch := reg.SubscribeDashboardStats("all")
	defer reg.UnsubscribeDashboardStats(ch)

	go reg.runDashboardSampler(10 * time.Millisecond)

	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("turning history off also stopped the live dashboard; the point of " +
			"the switch is to spend no disk, not to lose the screen")
	}

	if got := len(store.recorded()); got != 0 {
		t.Errorf("recorded %d samples with retention disabled; a sample written now is "+
			"a sample the purge has to delete later", got)
	}
}
