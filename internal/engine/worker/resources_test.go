package worker

import (
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// ---------------------------------------------------------------------------
// What a worker reports about the machine it is running on.
//
// These run against the real host rather than a fake, because the thing worth
// pinning is that the readings are plausible at all. A capacity probe that
// silently returns zero is indistinguishable from one that was never called,
// and zero is exactly what the dashboard then renders — a cluster with no CPU
// and no disk, which reads as a bug in the UI rather than in the probe.
// ---------------------------------------------------------------------------

func TestHostResources_ReportsThisMachine(t *testing.T) {
	res := hostResources(t.TempDir())

	if res.CPUCores <= 0 {
		t.Errorf("CPUCores = %d, want at least 1", res.CPUCores)
	}
	if res.MemoryTotalBytes <= 0 {
		t.Errorf("MemoryTotalBytes = %d, want a positive total", res.MemoryTotalBytes)
	}
	if res.MemoryUsedBytes <= 0 || res.MemoryUsedBytes > res.MemoryTotalBytes {
		t.Errorf("MemoryUsedBytes = %d, want 0 < used <= total (%d)", res.MemoryUsedBytes, res.MemoryTotalBytes)
	}
	if res.StorageTotalBytes <= 0 {
		t.Errorf("StorageTotalBytes = %d, want a positive total", res.StorageTotalBytes)
	}
	if res.StorageUsedBytes < 0 || res.StorageUsedBytes > res.StorageTotalBytes {
		t.Errorf("StorageUsedBytes = %d, want 0 <= used <= total (%d)", res.StorageUsedBytes, res.StorageTotalBytes)
	}
	if !res.ReportsCapacity() {
		t.Error("ReportsCapacity() is false, so this worker would be skipped by every cluster total")
	}
}

// The fractions are what the scheduler sheds load on, so they have to stay in
// 0..1 whatever the probes return.
func TestHostResources_UsageFractionsAreBounded(t *testing.T) {
	res := hostResources(t.TempDir())

	if res.CPUUsage < 0 || res.CPUUsage > 1 {
		t.Errorf("CPUUsage = %v, want 0..1", res.CPUUsage)
	}
	if res.MemoryUsage < 0 || res.MemoryUsage > 1 {
		t.Errorf("MemoryUsage = %v, want 0..1", res.MemoryUsage)
	}
	if got := res.StorageUsage(); got < 0 || got > 1 {
		t.Errorf("StorageUsage() = %v, want 0..1", got)
	}
}

// An unreadable path must not take the rest of the reading down with it. A
// worker whose data directory has been removed still has a CPU and memory, and
// reporting nothing at all would drop it out of the cluster totals entirely.
func TestHostResources_SurvivesAnUnreadableDataDir(t *testing.T) {
	res := hostResources("/this/path/does/not/exist")

	if res.CPUCores <= 0 || res.MemoryTotalBytes <= 0 {
		t.Errorf("a bad data dir wiped the CPU and memory readings: %+v", res)
	}
	if res.StorageTotalBytes != 0 || res.StorageUsedBytes != 0 {
		t.Errorf("StorageTotal/Used = %d/%d, want 0 for a path that cannot be read",
			res.StorageTotalBytes, res.StorageUsedBytes)
	}
}

// GetMetrics is what admission control reads, and it has to keep answering
// after the richer struct replaced the two floats behind it.
func TestWorkerResources_RoundTripThroughTheWorker(t *testing.T) {
	w := &Worker{}

	want := storage.WorkerResources{
		CPUUsage:          0.3,
		MemoryUsage:       0.6,
		CPUCores:          4,
		MemoryTotalBytes:  8 << 30,
		MemoryUsedBytes:   4 << 30,
		StorageTotalBytes: 100 << 30,
		StorageUsedBytes:  40 << 30,
	}
	w.SetResources(want)

	if got := w.Resources(); got != want {
		t.Errorf("Resources():\n got %+v\nwant %+v", got, want)
	}
	if cpu, mem := w.GetMetrics(); cpu != 0.3 || mem != 0.6 {
		t.Errorf("GetMetrics() = %v, %v, want 0.3, 0.6", cpu, mem)
	}

	// SetMetrics is the older, narrower entry point. It must move the two
	// fractions without discarding the capacity beside them — otherwise a
	// single admission-control update erases what the machine is.
	w.SetMetrics(0.9, 0.1)
	got := w.Resources()
	if got.CPUUsage != 0.9 || got.MemoryUsage != 0.1 {
		t.Errorf("SetMetrics did not update the fractions: %+v", got)
	}
	if got.CPUCores != 4 || got.MemoryTotalBytes != 8<<30 || got.StorageTotalBytes != 100<<30 {
		t.Errorf("SetMetrics discarded the capacity: %+v", got)
	}
}

// Registration persists what the machine is and says nothing about how busy it
// is — the rule storage.WorkerResources.Capacity exists for.
//
// The symptom when this breaks is not local. Every worker's view of itself
// comes from its local reading, which is empty until the first health check,
// so a real load figure written here makes each worker score itself as idle
// and every peer as loaded. They then all claim the same workflows, and the
// one that syncs first takes the lot. TestWorkerFailover catches that as a
// 10/0 split; this catches the cause.
func TestSelfRegister_PersistsCapacityWithoutALoadReading(t *testing.T) {
	store := &failoverStorage{
		workers:   make(map[string]storage.Worker),
		workflows: make(map[string]storage.Workflow),
		leases:    make(map[string]string),
	}
	w := NewWorker(store, nil)
	w.SetWorkerConfig(0, 1, "worker-1", "token")

	if err := w.SelfRegister(t.Context()); err != nil {
		t.Fatalf("SelfRegister: %v", err)
	}

	store.mu.Lock()
	got := store.workers["worker-1"]
	store.mu.Unlock()

	if got.CPUCores <= 0 || got.MemoryTotalBytes <= 0 {
		t.Errorf("registered without capacity: %+v", got.WorkerResources)
	}
	if got.CPUUsage != 0 || got.MemoryUsage != 0 {
		t.Errorf("registered with a load reading (cpu=%v mem=%v); every worker would then "+
			"score itself idle and its peers loaded", got.CPUUsage, got.MemoryUsage)
	}
	if cpu, mem := w.GetMetrics(); cpu != 0 || mem != 0 {
		t.Errorf("registration seeded admission control with a start-up reading: cpu=%v mem=%v", cpu, mem)
	}
}
