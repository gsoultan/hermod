package sql

import (
	"context"
	"database/sql"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// workerResourceColumns is the order the seven resource columns appear in
// QueryListWorkers, QueryGetWorker, QueryCreateWorker, QueryUpdateWorker and
// QueryUpdateHeartbeat.
//
// One order, named once. Three read sites and three write sites each spelled
// their own column list before this, and a column added to the SELECT but not
// to the Scan is a runtime error the compiler cannot see.
const workerResourceColumns = "cpu_usage, memory_usage, cpu_cores, " +
	"memory_total_bytes, memory_used_bytes, storage_total_bytes, storage_used_bytes"

// workerResourceScan receives those columns. Every one is nullable: the
// columns were added by autoMigrate to tables that already had rows, and a
// worker on a release from before capacity reporting never writes them.
type workerResourceScan struct {
	cpu, mem                  sql.NullFloat64
	cores                     sql.NullInt64
	memTotal, memUsed         sql.NullInt64
	storageTotal, storageUsed sql.NullInt64
}

func (r *workerResourceScan) dest() []any {
	return []any{&r.cpu, &r.mem, &r.cores, &r.memTotal, &r.memUsed, &r.storageTotal, &r.storageUsed}
}

func (r workerResourceScan) resources() storage.WorkerResources {
	return storage.WorkerResources{
		CPUUsage:          r.cpu.Float64,
		MemoryUsage:       r.mem.Float64,
		CPUCores:          int(r.cores.Int64),
		MemoryTotalBytes:  r.memTotal.Int64,
		MemoryUsedBytes:   r.memUsed.Int64,
		StorageTotalBytes: r.storageTotal.Int64,
		StorageUsedBytes:  r.storageUsed.Int64,
	}
}

// workerResourceArgs supplies the same seven values to a write, in the same
// order.
func workerResourceArgs(res storage.WorkerResources) []any {
	return []any{
		res.CPUUsage, res.MemoryUsage, res.CPUCores,
		res.MemoryTotalBytes, res.MemoryUsedBytes,
		res.StorageTotalBytes, res.StorageUsedBytes,
	}
}

// workerActiveWindow is how long a worker stays "online" after its last
// heartbeat, for both the count and the capacity totals beside it.
//
// The worker's own heartbeat interval is at most 30s (see heartbeatInterval),
// so two minutes tolerates three missed beats before a live worker disappears
// from the dashboard.
const workerActiveWindow = 2 * time.Minute

// readClusterResources totals the capacity of the workers that are currently
// online and folds it into stats.
//
// One aggregate query rather than reading the rows: the rows carry each
// worker's token, and pulling those through the process every five seconds to
// add up integers is a cost and an exposure with nothing to show for it.
//
// The CPU fraction is weighted by cores. A plain average over workers says a
// busy two-core box beside an idle thirty-core one is a cluster at 50%, which
// is the reading least likely to be acted on correctly; weighting says 6%,
// which is what is true. Workers reporting no cores are excluded from both
// sides of that fraction — see storage.WorkerResources on why zero is silence
// rather than an idle machine — so an old worker abstains instead of voting.
func (s *sqlStorage) readClusterResources(ctx context.Context, stats *storage.DashboardStats) error {
	const q = "SELECT " +
		"COUNT(*), " +
		"SUM(COALESCE(cpu_cores, 0)), " +
		"SUM(COALESCE(cpu_usage, 0) * COALESCE(cpu_cores, 0)), " +
		"SUM(COALESCE(memory_total_bytes, 0)), " +
		"SUM(COALESCE(memory_used_bytes, 0)), " +
		"SUM(COALESCE(storage_total_bytes, 0)), " +
		"SUM(COALESCE(storage_used_bytes, 0)) " +
		"FROM workers WHERE last_seen > ?"

	var (
		active                    int
		cores                     sql.NullInt64
		busyCores                 sql.NullFloat64
		memTotal, memUsed         sql.NullInt64
		storageTotal, storageUsed sql.NullInt64
	)
	err := s.queryRow(ctx, q, time.Now().Add(-workerActiveWindow)).
		Scan(&active, &cores, &busyCores, &memTotal, &memUsed, &storageTotal, &storageUsed)
	if err != nil {
		return err
	}

	stats.ActiveWorkers = active
	stats.CPUCores = int(cores.Int64)
	stats.MemoryTotalBytes = memTotal.Int64
	stats.MemoryUsedBytes = memUsed.Int64
	stats.StorageTotalBytes = storageTotal.Int64
	stats.StorageUsedBytes = storageUsed.Int64
	if cores.Int64 > 0 {
		stats.CPUUsage = busyCores.Float64 / float64(cores.Int64)
	}
	return nil
}
