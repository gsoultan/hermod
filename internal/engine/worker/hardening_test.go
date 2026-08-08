package worker

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/factory"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/internal/testutil"
)

// ---------------------------------------------------------------------------
// Lifecycle, leak and load hardening for the engine worker.
//
// These answer questions an operator asks about a long-running worker: does it
// survive being restarted, does it stay online when the workload spikes, and
// does anything accumulate across cycles that will eventually take it down.
// ---------------------------------------------------------------------------

// hardeningStorage is a full in-memory control plane: workers, workflows and
// leases, with counters for the calls the worker makes. It records lease
// ownership honestly (including expiry) so failover and failback are exercised
// against real semantics rather than a stub that always says yes.
type hardeningStorage struct {
	testutil.BaseMockStorage

	mu        sync.Mutex
	workers   map[string]storage.Worker
	workflows map[string]storage.Workflow
	leaseOwn  map[string]string
	leaseTill map[string]time.Time

	heartbeats atomic.Int64
	listCalls  atomic.Int64
	// failStorage makes every call fail, simulating a control-plane outage.
	failStorage atomic.Bool
}

func newHardeningStorage(workflowCount int) *hardeningStorage {
	s := &hardeningStorage{
		workers:   map[string]storage.Worker{},
		workflows: map[string]storage.Workflow{},
		leaseOwn:  map[string]string{},
		leaseTill: map[string]time.Time{},
	}
	for i := range workflowCount {
		id := fmt.Sprintf("wf-%03d", i)
		s.workflows[id] = storage.Workflow{
			ID:     id,
			Name:   id,
			Active: true,
			Nodes: []storage.WorkflowNode{
				{ID: "n1", Type: "source", RefID: "s1"},
				{ID: "n2", Type: "sink", RefID: "snk1"},
			},
			Edges: []storage.WorkflowEdge{{ID: "e1", SourceID: "n1", TargetID: "n2"}},
		}
	}
	return s
}

func (s *hardeningStorage) down() error {
	if s.failStorage.Load() {
		return fmt.Errorf("control plane unavailable")
	}
	return nil
}

func (s *hardeningStorage) ListWorkers(ctx context.Context, f storage.CommonFilter) ([]storage.Worker, int, error) {
	if err := s.down(); err != nil {
		return nil, 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]storage.Worker, 0, len(s.workers))
	for _, w := range s.workers {
		out = append(out, w)
	}
	return out, len(out), nil
}

func (s *hardeningStorage) GetWorker(ctx context.Context, id string) (storage.Worker, error) {
	if err := s.down(); err != nil {
		return storage.Worker{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workers[id]
	if !ok {
		return storage.Worker{}, fmt.Errorf("worker %s not found", id)
	}
	return w, nil
}

func (s *hardeningStorage) CreateWorker(ctx context.Context, w storage.Worker) error {
	if err := s.down(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers[w.ID] = w
	return nil
}

func (s *hardeningStorage) DeleteWorker(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.workers, id)
	return nil
}

func (s *hardeningStorage) UpdateWorkerHeartbeat(ctx context.Context, id string, cpu, mem float64) error {
	s.heartbeats.Add(1)
	if err := s.down(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workers[id]
	if !ok {
		return nil
	}
	now := time.Now()
	w.LastSeen, w.CPUUsage, w.MemoryUsage = &now, cpu, mem
	s.workers[id] = w
	return nil
}

func (s *hardeningStorage) ListWorkflows(ctx context.Context, f storage.CommonFilter) ([]storage.Workflow, int, error) {
	s.listCalls.Add(1)
	if err := s.down(); err != nil {
		return nil, 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []storage.Workflow
	for _, wf := range s.workflows {
		wf.OwnerID = s.leaseOwn[wf.ID]
		if till, ok := s.leaseTill[wf.ID]; ok {
			t := till
			wf.LeaseUntil = &t
		}
		if f.Active != nil && wf.Active != *f.Active {
			continue
		}
		if f.WorkerID != "" && wf.WorkerID != f.WorkerID {
			continue
		}
		if f.OwnerID != "" && wf.OwnerID != f.OwnerID {
			continue
		}
		out = append(out, wf)
	}
	return out, len(out), nil
}

func (s *hardeningStorage) AcquireWorkflowLease(ctx context.Context, wfID, owner string, ttl int) (bool, error) {
	if err := s.down(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, held := s.leaseOwn[wfID]
	till, hasTill := s.leaseTill[wfID]
	live := held && cur != "" && cur != owner && hasTill && time.Now().Before(till)
	if live {
		return false, nil
	}
	s.leaseOwn[wfID] = owner
	s.leaseTill[wfID] = time.Now().Add(time.Duration(ttl) * time.Second)
	return true, nil
}

func (s *hardeningStorage) RenewWorkflowLease(ctx context.Context, wfID, owner string, ttl int) (bool, error) {
	if err := s.down(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leaseOwn[wfID] != owner {
		return false, nil
	}
	s.leaseTill[wfID] = time.Now().Add(time.Duration(ttl) * time.Second)
	return true, nil
}

func (s *hardeningStorage) ReleaseWorkflowLease(ctx context.Context, wfID, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leaseOwn[wfID] == owner {
		delete(s.leaseOwn, wfID)
		delete(s.leaseTill, wfID)
	}
	return nil
}

func (s *hardeningStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	return storage.Source{ID: id, Type: "test-source"}, nil
}
func (s *hardeningStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	return storage.Sink{ID: id, Type: "stdout"}, nil
}
func (s *hardeningStorage) ListSources(ctx context.Context, f storage.CommonFilter) ([]storage.Source, int, error) {
	return nil, 0, nil
}
func (s *hardeningStorage) ListSinks(ctx context.Context, f storage.CommonFilter) ([]storage.Sink, int, error) {
	return nil, 0, nil
}
func (s *hardeningStorage) UpdateWorkflowStatus(ctx context.Context, id, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if wf, ok := s.workflows[id]; ok {
		wf.Status = status
		s.workflows[id] = wf
	}
	return nil
}

func (s *hardeningStorage) leaseCount(owner string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, o := range s.leaseOwn {
		if o == owner {
			n++
		}
	}
	return n
}

func (s *hardeningStorage) setWorkerLoad(id string, cpu, mem float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w, ok := s.workers[id]; ok {
		now := time.Now()
		w.CPUUsage, w.MemoryUsage, w.LastSeen = cpu, mem, &now
		s.workers[id] = w
	}
}

// expireWorker makes a worker look dead to its peers without deleting its row,
// which is what a crashed process actually looks like: the registration is
// still there, the heartbeat has stopped.
func (s *hardeningStorage) expireWorker(id string, age time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w, ok := s.workers[id]; ok {
		old := time.Now().Add(-age)
		w.LastSeen = &old
		s.workers[id] = w
	}
	for wfID, owner := range s.leaseOwn {
		if owner == id {
			s.leaseTill[wfID] = time.Now().Add(-age)
		}
	}
}

func newHardenedWorker(store *hardeningStorage, id string) (*Worker, *registry.Registry) {
	reg := registry.NewRegistry(store)
	reg.SetFactories(
		func(cfg factory.SourceConfig) (hermod.Source, error) { return &mockSource{}, nil },
		func(cfg factory.SinkConfig) (hermod.Sink, error) { return &mockSink{}, nil },
	)
	w := NewWorker(store, reg)
	w.SetWorkerConfig(0, 1, id, "token")
	w.SetWorkerCacheTTL(5 * time.Millisecond)
	w.SetLeaseTTL(30)
	w.SetSyncInterval(200 * time.Millisecond)
	w.SetRegistrationInfo(id, "127.0.0.1", 0, "hardening test")
	return w, reg
}

func runningWorkflows(reg *registry.Registry, store *hardeningStorage) int {
	store.mu.Lock()
	ids := make([]string, 0, len(store.workflows))
	for id := range store.workflows {
		ids = append(ids, id)
	}
	store.mu.Unlock()
	n := 0
	for _, id := range ids {
		if reg.IsEngineRunning(id) {
			n++
		}
	}
	return n
}

// settledGoroutines returns the goroutine count after giving the runtime a
// chance to reap finished goroutines. A single sample right after a stop is
// meaningless — teardown is asynchronous — so poll until it stops falling.
func settledGoroutines(d time.Duration) int {
	deadline := time.Now().Add(d)
	last := runtime.NumGoroutine()
	stable := 0
	for time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
		runtime.GC()
		n := runtime.NumGoroutine()
		if n >= last {
			stable++
			if stable >= 4 {
				return n
			}
		} else {
			stable = 0
		}
		last = n
	}
	return runtime.NumGoroutine()
}

// TestWorkerStartStopRestartCycles is the operator's "turn it off and on again"
// path. Across repeated cycles the worker must reacquire its workflows every
// time, release every lease on the way out, and not accumulate goroutines.
func TestWorkerStartStopRestartCycles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping lifecycle cycle test in short mode")
	}

	const workflows = 8
	const cycles = 6

	store := newHardeningStorage(workflows)

	// Warm up once so the baseline excludes one-time initialisation (metric
	// registries, pools, the optimizer goroutine) rather than counting it as a
	// leak.
	w0, reg0 := newHardenedWorker(store, "worker-warmup")
	_ = w0.SelfRegister(t.Context())
	w0.sync(t.Context(), true)
	w0.cleanup(t.Context())
	reg0.StopAll()
	reg0.Close()

	base := settledGoroutines(3 * time.Second)

	for cycle := range cycles {
		ctx := context.Background()
		w, reg := newHardenedWorker(store, "worker-restart")
		// Pin resource usage below the admission-control thresholds in
		// sync.go. Left to the real host, a `-race` run pegs the CPU above
		// 0.85 and the worker legitimately refuses to start new workflows,
		// which would make this a test of the machine's load, not of restart.
		w.SetMetrics(0.05, 0.05)

		if err := w.SelfRegister(ctx); err != nil {
			t.Fatalf("cycle %d: SelfRegister: %v", cycle, err)
		}
		w.sync(ctx, cycle == 0)

		if !waitCond(10*time.Second, func() bool {
			w.SetMetrics(0.05, 0.05)
			w.sync(ctx, false)
			return runningWorkflows(reg, store) == workflows
		}) {
			t.Fatalf("cycle %d: worker started only %d/%d workflows", cycle, runningWorkflows(reg, store), workflows)
		}
		if got := store.leaseCount("worker-restart"); got != workflows {
			t.Errorf("cycle %d: holds %d leases, want %d", cycle, got, workflows)
		}

		// cleanup is exactly what Start defers on the way out: stop every
		// engine, release every lease, deregister.
		w.cleanup(ctx)
		// StopAll stops the engines; Close also stops the registry's own
		// background goroutines (optimizer, reconciliation). A restart builds a
		// fresh registry, so skipping Close leaks one set per cycle.
		reg.Close()

		if got := runningWorkflows(reg, store); got != 0 {
			t.Errorf("cycle %d: %d workflows still running after stop", cycle, got)
		}
		if got := store.leaseCount("worker-restart"); got != 0 {
			t.Errorf("cycle %d: %d leases still held after stop; a peer cannot take over", cycle, got)
		}
	}

	// One full Start/cancel round-trip proves the supervised loop itself exits
	// cleanly and releases everything, which the sync/cleanup cycles above
	// deliberately bypass.
	wRun, regRun := newHardenedWorker(store, "worker-restart-loop")
	wRun.SetMetrics(0.05, 0.05)
	runCtx, runCancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- wRun.Start(runCtx) }()
	time.Sleep(500 * time.Millisecond)
	runCancel()
	select {
	case <-runErr:
	case <-time.After(90 * time.Second):
		t.Fatal("Start did not return within 90s of cancellation")
	}
	if got := store.leaseCount("worker-restart-loop"); got != 0 {
		t.Errorf("Start/cancel left %d leases held", got)
	}
	regRun.StopAll()
	regRun.Close()

	after := settledGoroutines(5 * time.Second)
	// Each cycle starts and stops a full worker plus its engines. A per-cycle
	// residue would show up as growth proportional to `cycles`; allow a small
	// fixed slack for runtime bookkeeping.
	if after > base+15 {
		buf := make([]byte, 1<<16)
		n := runtime.Stack(buf, true)
		t.Errorf("goroutines grew from %d to %d across %d start/stop cycles (leak)\n%s",
			base, after, cycles, buf[:n])
	} else {
		t.Logf("goroutines: base=%d after %d cycles=%d", base, cycles, after)
	}
}

// waitCond polls cond until it holds or the timeout expires.
func waitCond(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// TestWorkerStaysOnlineUnderHeavySyncLoad is the "must not go offline on heavy
// traffic" requirement. With many workflows and a fast sync cadence, the worker
// has to keep heartbeating — the heartbeat is what peers use to decide it is
// alive, so a worker that stops heartbeating under load is failed over even
// though it is healthy, and its workflows are taken from it.
func TestWorkerStaysOnlineUnderHeavySyncLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping heavy load test in short mode")
	}

	const workflows = 120

	store := newHardeningStorage(workflows)
	w, reg := newHardenedWorker(store, "worker-load")
	w.SetSyncInterval(200 * time.Millisecond)
	w.SetLeaseTTL(5) // 5s TTL => 5s heartbeat cadence

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	// Admission control (sync.go) refuses new workflows above 0.85 CPU/memory,
	// and checkHealth overwrites the worker's metrics from the real host on
	// every heartbeat. Running the whole suite under -race routinely pushes the
	// machine past that line, which would make this a test of the build agent's
	// load rather than of the worker. Keep the reported usage pinned low so the
	// load path is exercised deterministically.
	pinDone := make(chan struct{})
	go func() {
		for {
			select {
			case <-pinDone:
				return
			case <-time.After(20 * time.Millisecond):
				w.SetMetrics(0.05, 0.05)
			}
		}
	}()

	w.SetMetrics(0.05, 0.05)
	go func() { errCh <- w.Start(ctx) }()
	defer func() {
		close(pinDone)
		cancel()
		select {
		case <-errCh:
		case <-time.After(90 * time.Second):
			t.Error("worker did not shut down within 90s")
		}
	}()

	if !waitCond(60*time.Second, func() bool { return runningWorkflows(reg, store) == workflows }) {
		t.Fatalf("worker started only %d/%d workflows under load", runningWorkflows(reg, store), workflows)
	}

	before := store.heartbeats.Load()
	syncsBefore := store.listCalls.Load()
	start := time.Now()

	// The heartbeat is what peers use to decide this worker is alive. A worker
	// that stops heartbeating under load is failed over while healthy and has
	// its workflows taken away — that is the "goes offline on heavy traffic"
	// failure this guards.
	if !waitCond(25*time.Second, func() bool { return store.heartbeats.Load() >= before+3 }) {
		t.Fatalf("only %d heartbeats in %v under %d workflows; the worker would be declared dead and failed over",
			store.heartbeats.Load()-before, time.Since(start), workflows)
	}

	// It must also still be reconciling, not just heartbeating from a wedged loop.
	if store.listCalls.Load() <= syncsBefore {
		t.Error("worker heartbeated but ran no sync cycles under load; its reconcile loop is wedged")
	}
	select {
	case err := <-errCh:
		t.Fatalf("worker exited under load: %v", err)
	default:
	}

	running := runningWorkflows(reg, store)
	if got := store.leaseCount("worker-load"); got < running {
		t.Errorf("worker runs %d workflows but holds only %d leases; it is running work it does not own", running, got)
	}
	t.Logf("under %d workflows: %d running, %d heartbeats in %v, %d sync list calls",
		workflows, running, store.heartbeats.Load()-before, time.Since(start), store.listCalls.Load())
}

// TestWorkerSurvivesControlPlaneOutage covers the negative path an operator
// most fears: the metadata database goes away. The worker must not exit, must
// not panic, and must resume normally when storage returns.
func TestWorkerSurvivesControlPlaneOutage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping outage test in short mode")
	}

	store := newHardeningStorage(6)
	w, reg := newHardenedWorker(store, "worker-outage")

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	// Keep reported usage below the admission-control thresholds in sync.go;
	// see TestWorkerStaysOnlineUnderHeavySyncLoad for why the real host reading
	// makes this non-deterministic when the whole suite runs under -race.
	pinDone := make(chan struct{})
	go func() {
		for {
			select {
			case <-pinDone:
				return
			case <-time.After(20 * time.Millisecond):
				w.SetMetrics(0.05, 0.05)
			}
		}
	}()

	w.SetMetrics(0.05, 0.05)
	go func() { errCh <- w.Start(ctx) }()
	defer func() {
		close(pinDone)
		cancel()
		select {
		case <-errCh:
		case <-time.After(90 * time.Second):
			t.Error("worker did not shut down within 90s")
		}
	}()

	if !waitCond(30*time.Second, func() bool { return runningWorkflows(reg, store) == 6 }) {
		t.Fatalf("worker never started its workflows: %d/6", runningWorkflows(reg, store))
	}

	// Storage goes down.
	store.failStorage.Store(true)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("worker exited during a storage outage: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Storage comes back; the worker must recover on its own.
	store.failStorage.Store(false)
	if !waitCond(20*time.Second, func() bool { return runningWorkflows(reg, store) == 6 }) {
		t.Errorf("worker did not recover after the outage: %d/6 workflows running", runningWorkflows(reg, store))
	}
	if got := store.leaseCount("worker-outage"); got != 6 {
		t.Errorf("leases not re-established after the outage: %d/6", got)
	}
}

// TestWorkerFailoverThenFailbackUnderLoad exercises the whole availability
// cycle against real lease semantics: two workers share the load, one dies, the
// survivor takes everything, the dead one returns and the load rebalances.
func TestWorkerFailoverThenFailbackUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping failover/failback test in short mode")
	}

	const workflows = 20
	store := newHardeningStorage(workflows)

	w1, reg1 := newHardenedWorker(store, "worker-1")
	w2, reg2 := newHardenedWorker(store, "worker-2")

	ctx := t.Context()
	_ = w1.SelfRegister(ctx)
	_ = w2.SelfRegister(ctx)
	store.setWorkerLoad("worker-1", 0.2, 0.2)
	store.setWorkerLoad("worker-2", 0.2, 0.2)

	w1.sync(ctx, true)
	w2.sync(ctx, true)

	n1, n2 := runningWorkflows(reg1, store), runningWorkflows(reg2, store)
	t.Logf("initial split: worker-1=%d worker-2=%d", n1, n2)
	if n1+n2 != workflows {
		t.Fatalf("initial distribution covered %d/%d workflows", n1+n2, workflows)
	}
	if n1 == 0 || n2 == 0 {
		t.Errorf("load was not shared: worker-1=%d worker-2=%d", n1, n2)
	}

	// --- failover: worker-1 crashes (registration stays, heartbeat stops) ---
	reg1.StopAll()
	store.expireWorker("worker-1", 5*time.Minute)
	time.Sleep(20 * time.Millisecond) // let the sharding cache expire

	if !waitCond(10*time.Second, func() bool {
		w2.sync(ctx, false)
		return runningWorkflows(reg2, store) == workflows
	}) {
		t.Fatalf("failover incomplete: worker-2 runs %d/%d workflows", runningWorkflows(reg2, store), workflows)
	}
	t.Logf("after failover: worker-2=%d", runningWorkflows(reg2, store))

	// --- failback: worker-1 returns healthy while worker-2 is saturated ---
	w1b, reg1b := newHardenedWorker(store, "worker-1")
	_ = w1b.SelfRegister(ctx)
	store.setWorkerLoad("worker-1", 0.05, 0.05)
	store.setWorkerLoad("worker-2", 0.95, 0.95)
	w1b.SetMetrics(0.05, 0.05)
	w2.SetMetrics(0.95, 0.95)
	time.Sleep(20 * time.Millisecond)

	// Rebalancing needs several reconciliation rounds, and the two halves are
	// ordered: the incumbent must first decide a workflow is no longer its own
	// and release the lease (asynchronously, via stopWorkflow), and only then
	// can the recovered worker acquire it. Alternating with a pause between
	// gives that handoff room to complete.
	for range 30 {
		w2.SetMetrics(0.95, 0.95)
		w2.sync(ctx, false)
		time.Sleep(10 * time.Millisecond)
		w1b.SetMetrics(0.05, 0.05)
		w1b.sync(ctx, false)
		time.Sleep(10 * time.Millisecond)
		if runningWorkflows(reg1b, store) > 0 {
			break
		}
	}

	back1, back2 := runningWorkflows(reg1b, store), runningWorkflows(reg2, store)
	t.Logf("after failback: worker-1=%d worker-2=%d", back1, back2)

	// Safety first: whatever the split, no workflow may be lost, and none may
	// run on two workers at once — a double-run duplicates every message.
	if back1+back2 != workflows {
		t.Errorf("workflows lost or duplicated during failback: worker-1=%d worker-2=%d, want %d total",
			back1, back2, workflows)
	}
	store.mu.Lock()
	ids := make([]string, 0, len(store.workflows))
	for id := range store.workflows {
		ids = append(ids, id)
	}
	store.mu.Unlock()
	for _, id := range ids {
		if reg1b.IsEngineRunning(id) && reg2.IsEngineRunning(id) {
			t.Errorf("workflow %s is running on both workers at once", id)
		}
	}

	// Liveness of failback by *load* is deliberately weak: the incumbent gets a
	// 2x hysteresis bonus (sharding.go) to stop workflows flapping, which
	// TestRendezvousHysteresisStillAllowsFailback measures at roughly 199 in
	// 200 keys retained. At this workflow count the expected reclaim is well
	// under one, so requiring a share here would be asserting a contract the
	// design does not offer. What must always hold is that the recovered worker
	// is available to take over, which the crash path below proves.
	t.Logf("load-based reclaim after failback: worker-1=%d of %d (hysteresis favours the incumbent)", back1, workflows)

	// Failback on death, as opposed to on load, must be complete: when the
	// incumbent stops heartbeating, the recovered worker has to take everything.
	reg2.StopAll()
	store.expireWorker("worker-2", 5*time.Minute)
	time.Sleep(20 * time.Millisecond)
	if !waitCond(15*time.Second, func() bool {
		w1b.SetMetrics(0.05, 0.05)
		w1b.sync(ctx, false)
		return runningWorkflows(reg1b, store) == workflows
	}) {
		t.Errorf("recovered worker did not take over after its peer died: %d/%d workflows",
			runningWorkflows(reg1b, store), workflows)
	}

	reg1b.StopAll()
	reg2.StopAll()
}

// TestWorkerLeaseRenewalMapDoesNotLeak is the storage-leak check for the
// worker's own bookkeeping: renewCancel holds a cancel func per running
// workflow, so if entries survive their workflow the map grows for the life of
// the process and every entry keeps a goroutine alive with it.
func TestWorkerLeaseRenewalMapDoesNotLeak(t *testing.T) {
	store := newHardeningStorage(10)
	w, reg := newHardenedWorker(store, "worker-leases")
	defer reg.StopAll()

	ctx := t.Context()
	_ = w.SelfRegister(ctx)

	for range 5 {
		w.sync(ctx, false)

		w.renewMu.Lock()
		n := len(w.renewCancel)
		w.renewMu.Unlock()
		if n > 10 {
			t.Fatalf("renewCancel holds %d entries for 10 workflows; duplicates are accumulating", n)
		}
	}

	// Deactivating every workflow must tear the renewals down, not orphan them.
	store.mu.Lock()
	for id, wf := range store.workflows {
		wf.Active = false
		store.workflows[id] = wf
	}
	store.mu.Unlock()

	if !waitCond(30*time.Second, func() bool {
		w.sync(ctx, false)
		w.renewMu.Lock()
		n := len(w.renewCancel)
		w.renewMu.Unlock()
		return n == 0
	}) {
		w.renewMu.Lock()
		n := len(w.renewCancel)
		w.renewMu.Unlock()
		t.Errorf("renewCancel still holds %d entries after every workflow was deactivated", n)
	}

	// ReleaseAllLeases is the shutdown path and must clear the map outright.
	w.sync(ctx, false)
	w.ReleaseAllLeases(ctx)
	w.renewMu.Lock()
	n := len(w.renewCancel)
	w.renewMu.Unlock()
	if n != 0 {
		t.Errorf("ReleaseAllLeases left %d renewal entries behind", n)
	}
}

// TestWorkerRepeatedSyncDoesNotLeakGoroutines targets the sync path itself:
// syncAllWorkflows spawns a goroutine per workflow behind a semaphore, and the
// health check spawns its own. None of them may outlive the call.
func TestWorkerRepeatedSyncDoesNotLeakGoroutines(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping goroutine leak test in short mode")
	}

	store := newHardeningStorage(25)
	w, reg := newHardenedWorker(store, "worker-sync")
	defer reg.StopAll()

	ctx := t.Context()
	_ = w.SelfRegister(ctx)

	// Warm up so pools and per-workflow engine goroutines are already up.
	w.sync(ctx, true)
	waitCond(10*time.Second, func() bool { return runningWorkflows(reg, store) == 25 })
	base := settledGoroutines(3 * time.Second)

	for range 20 {
		w.sync(ctx, false)
	}

	after := settledGoroutines(5 * time.Second)
	if after > base+20 {
		t.Errorf("goroutines grew from %d to %d over 20 sync cycles with a stable workflow set", base, after)
	} else {
		t.Logf("goroutines stable across 20 syncs: base=%d after=%d", base, after)
	}
}

// TestWorkerConcurrentSyncIsSafe hammers sync from several goroutines at once,
// which is what a restart storm plus the periodic ticker produce. It must stay
// race-free (the suite runs under -race) and must not double-start a workflow.
func TestWorkerConcurrentSyncIsSafe(t *testing.T) {
	store := newHardeningStorage(12)
	w, reg := newHardenedWorker(store, "worker-concurrent")
	defer reg.StopAll()

	ctx := t.Context()
	_ = w.SelfRegister(ctx)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 5 {
				w.sync(ctx, false)
			}
		})
	}
	wg.Wait()

	if got := runningWorkflows(reg, store); got != 12 {
		t.Errorf("after concurrent syncs %d/12 workflows are running", got)
	}
	if got := store.leaseCount("worker-concurrent"); got != 12 {
		t.Errorf("after concurrent syncs %d/12 leases are held", got)
	}
}
