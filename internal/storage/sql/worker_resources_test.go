package sql

import (
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"

	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Worker capacity: cores, memory and disk, alongside the usage fractions that
// were already here.
//
// The usage fractions on their own were not enough to answer the question the
// workers page exists to answer. "80% CPU" is the same reading on two cores and
// on sixty-four, and a worker about to run out of disk looked exactly like one
// with a terabyte free — the dashboard had no disk reading at all. These tests
// pin the capacity down the persistence path and back out again, and pin the
// cluster totals the dashboard shows.
// ---------------------------------------------------------------------------

// heartbeatNow is the clock a last_seen stamp has to come off.
//
// Local time, not UTC: modernc/sqlite stores a time.Time as its String() form,
// offset and all, so "last_seen > ?" is a lexicographic comparison of two
// formatted strings. Production stamps last_seen with a plain time.Now() on
// both sides of that predicate; a test that stamped UTC would be comparing
// "+0000 UTC" against a "+0700 WIB" threshold and measuring the driver rather
// than the query.
func heartbeatNow() time.Time { return time.Now() }

// fullyLoadedWorker is a worker reporting every resource field, used so a test
// that drops one on the floor fails rather than passing on a zero.
func fullyLoadedWorker(id string, seen time.Time) storage.Worker {
	return storage.Worker{
		ID:       id,
		Name:     id,
		Host:     "10.0.0.1",
		Port:     8080,
		Token:    "t-" + id,
		LastSeen: &seen,
		WorkerResources: storage.WorkerResources{
			CPUUsage:          0.25,
			MemoryUsage:       0.5,
			CPUCores:          8,
			MemoryTotalBytes:  32 << 30,
			MemoryUsedBytes:   16 << 30,
			StorageTotalBytes: 500 << 30,
			StorageUsedBytes:  125 << 30,
		},
	}
}

func TestWorkerResources_SurviveCreateAndRead(t *testing.T) {
	ctx := t.Context()
	s := newDashboardHistoryStorage(t)

	seen := heartbeatNow().Truncate(time.Second)
	want := fullyLoadedWorker("w1", seen)
	if err := s.CreateWorker(ctx, want); err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}

	got, err := s.GetWorker(ctx, "w1")
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if got.WorkerResources != want.WorkerResources {
		t.Errorf("GetWorker resources:\n got %+v\nwant %+v", got.WorkerResources, want.WorkerResources)
	}

	listed, _, err := s.ListWorkers(ctx, storage.CommonFilter{Limit: -1})
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(listed))
	}
	if listed[0].WorkerResources != want.WorkerResources {
		t.Errorf("ListWorkers resources:\n got %+v\nwant %+v", listed[0].WorkerResources, want.WorkerResources)
	}
}

// A heartbeat is the only write that happens while a worker is running, so if
// it does not carry capacity then capacity is whatever was true at registration
// — which for a worker registered through the UI is nothing at all.
func TestUpdateWorkerHeartbeat_PersistsCapacityNotJustUsage(t *testing.T) {
	ctx := t.Context()
	s := newDashboardHistoryStorage(t)

	if err := s.CreateWorker(ctx, storage.Worker{ID: "w1", Name: "w1"}); err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}

	want := storage.WorkerResources{
		CPUUsage:          0.75,
		MemoryUsage:       0.4,
		CPUCores:          16,
		MemoryTotalBytes:  64 << 30,
		MemoryUsedBytes:   26 << 30,
		StorageTotalBytes: 1 << 40,
		StorageUsedBytes:  300 << 30,
	}
	if err := s.UpdateWorkerHeartbeat(ctx, "w1", want); err != nil {
		t.Fatalf("UpdateWorkerHeartbeat: %v", err)
	}

	got, err := s.GetWorker(ctx, "w1")
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if got.WorkerResources != want {
		t.Errorf("after heartbeat:\n got %+v\nwant %+v", got.WorkerResources, want)
	}
	if got.LastSeen == nil {
		t.Error("heartbeat did not stamp last_seen")
	}
}

// The dashboard totals describe the machines that are actually there. A worker
// that stopped reporting two minutes ago is already excluded from ActiveWorkers;
// counting its cores and disk would describe a cluster that no longer exists.
func TestGetDashboardStats_CountsResourcesOfOnlineWorkersOnly(t *testing.T) {
	ctx := t.Context()
	s := newDashboardHistoryStorage(t)

	now := heartbeatNow()
	online := fullyLoadedWorker("online", now)
	stale := fullyLoadedWorker("stale", now.Add(-10*time.Minute))
	for _, w := range []storage.Worker{online, stale} {
		if err := s.CreateWorker(ctx, w); err != nil {
			t.Fatalf("CreateWorker %s: %v", w.ID, err)
		}
	}

	stats, err := s.GetDashboardStats(ctx, "")
	if err != nil {
		t.Fatalf("GetDashboardStats: %v", err)
	}

	if stats.ActiveWorkers != 1 {
		t.Fatalf("ActiveWorkers = %d, want 1", stats.ActiveWorkers)
	}
	if stats.CPUCores != 8 {
		t.Errorf("CPUCores = %d, want 8 (the stale worker's cores must not count)", stats.CPUCores)
	}
	if stats.MemoryTotalBytes != 32<<30 {
		t.Errorf("MemoryTotalBytes = %d, want %d", stats.MemoryTotalBytes, int64(32<<30))
	}
	if stats.MemoryUsedBytes != 16<<30 {
		t.Errorf("MemoryUsedBytes = %d, want %d", stats.MemoryUsedBytes, int64(16<<30))
	}
	if stats.StorageTotalBytes != 500<<30 {
		t.Errorf("StorageTotalBytes = %d, want %d", stats.StorageTotalBytes, int64(500<<30))
	}
	if stats.StorageUsedBytes != 125<<30 {
		t.Errorf("StorageUsedBytes = %d, want %d", stats.StorageUsedBytes, int64(125<<30))
	}
	if stats.CPUUsage != 0.25 {
		t.Errorf("CPUUsage = %v, want 0.25", stats.CPUUsage)
	}
}

// Two workers, one small and busy and one large and idle. A plain average says
// the cluster is half used; weighting by cores says what is actually true,
// which is that most of the compute in the room is doing nothing.
func TestGetDashboardStats_CPUUsageIsWeightedByCores(t *testing.T) {
	ctx := t.Context()
	s := newDashboardHistoryStorage(t)

	now := heartbeatNow()
	small := fullyLoadedWorker("small", now)
	small.CPUCores = 2
	small.CPUUsage = 1.0
	big := fullyLoadedWorker("big", now)
	big.CPUCores = 30
	big.CPUUsage = 0.0

	for _, w := range []storage.Worker{small, big} {
		if err := s.CreateWorker(ctx, w); err != nil {
			t.Fatalf("CreateWorker %s: %v", w.ID, err)
		}
	}

	stats, err := s.GetDashboardStats(ctx, "")
	if err != nil {
		t.Fatalf("GetDashboardStats: %v", err)
	}
	if stats.CPUCores != 32 {
		t.Fatalf("CPUCores = %d, want 32", stats.CPUCores)
	}
	// 2 busy cores out of 32.
	if got, want := stats.CPUUsage, 2.0/32.0; got != want {
		t.Errorf("CPUUsage = %v, want %v (a plain average would say 0.5)", got, want)
	}
}

// A worker running a release from before capacity reporting existed writes
// NULL into these columns. Zero cores must read as "did not say", not as a
// machine with no CPU — otherwise one old worker drags the weighted reading of
// the whole cluster to zero.
func TestGetDashboardStats_WorkersThatReportNoCapacityDoNotSkewIt(t *testing.T) {
	ctx := t.Context()
	s := newDashboardHistoryStorage(t)

	now := heartbeatNow()
	reporting := fullyLoadedWorker("reporting", now)
	silent := storage.Worker{ID: "silent", Name: "silent", LastSeen: &now}
	for _, w := range []storage.Worker{reporting, silent} {
		if err := s.CreateWorker(ctx, w); err != nil {
			t.Fatalf("CreateWorker %s: %v", w.ID, err)
		}
	}

	stats, err := s.GetDashboardStats(ctx, "")
	if err != nil {
		t.Fatalf("GetDashboardStats: %v", err)
	}
	if stats.ActiveWorkers != 2 {
		t.Fatalf("ActiveWorkers = %d, want 2", stats.ActiveWorkers)
	}
	if stats.CPUCores != 8 {
		t.Errorf("CPUCores = %d, want 8", stats.CPUCores)
	}
	if stats.CPUUsage != 0.25 {
		t.Errorf("CPUUsage = %v, want 0.25 — the silent worker must not count as an idle one", stats.CPUUsage)
	}
}

// Nothing registered at all is a legitimate state, and must not divide by zero
// or report a NaN the JSON encoder then refuses to marshal.
func TestGetDashboardStats_NoWorkersReportsZeroResources(t *testing.T) {
	ctx := t.Context()
	s := newDashboardHistoryStorage(t)

	stats, err := s.GetDashboardStats(ctx, "")
	if err != nil {
		t.Fatalf("GetDashboardStats: %v", err)
	}
	if stats.CPUCores != 0 || stats.CPUUsage != 0 || stats.MemoryTotalBytes != 0 || stats.StorageTotalBytes != 0 {
		t.Errorf("empty cluster reported resources: %+v", stats)
	}
}
