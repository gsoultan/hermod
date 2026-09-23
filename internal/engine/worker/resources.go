package worker

import (
	"runtime"
	"time"

	"github.com/gsoultan/hermod/internal/config"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

// cpuSampleWindow is how long the CPU probe watches before answering.
//
// cpu.Percent needs an interval to difference two readings against; with zero
// it reports the average since the process started, which on a long-lived
// worker is a number that stops moving. A tenth of a second is long enough to
// be a measurement and short enough that the health check is not sitting in it.
const cpuSampleWindow = 100 * time.Millisecond

// currentHostResources reads the machine this worker is running on, measuring
// storage on the filesystem Hermod actually writes to.
//
// The directory is resolved here rather than at each call site so that the two
// callers — registration and the health check — cannot disagree about which
// filesystem the numbers describe, and so worker.go does not need an import
// that collides with pkg/engine/config.
func currentHostResources() storage.WorkerResources {
	return hostResources(config.GetConfigDir())
}

// hostResources reads what this machine has and how much of it is in use.
//
// dataDir is the directory whose filesystem is reported as storage — where
// Hermod writes its metadata database, WAL files and trace payloads. Not every
// disk on the box: a worker with three mounted volumes and a full data
// directory is out of room, and a total across all three would say it has
// plenty.
//
// Every probe degrades on its own. A failed disk reading leaves the storage
// fields zero and keeps the CPU and memory readings, because a worker whose
// data directory has been removed still has a CPU, and reporting nothing at all
// would drop it out of the cluster totals entirely — see
// storage.WorkerResources on zero meaning "did not say".
//
// Containers are the known caveat: gopsutil reads /proc/meminfo, which reports
// the host's memory rather than the cgroup limit, so a worker in a memory-capped
// container reports the machine it is sharing. That is still the number an
// operator needs to size the host, and it is the same figure the process itself
// sees when it allocates.
func hostResources(dataDir string) storage.WorkerResources {
	res := storage.WorkerResources{
		CPUCores: logicalCores(),
	}

	if v, err := mem.VirtualMemory(); err == nil && v != nil {
		res.MemoryTotalBytes = int64(v.Total)
		res.MemoryUsedBytes = int64(v.Used)
		res.MemoryUsage = clampFraction(v.UsedPercent / 100.0)
	}

	if u, err := disk.Usage(dataDir); err == nil && u != nil {
		res.StorageTotalBytes = int64(u.Total)
		res.StorageUsedBytes = int64(u.Used)
	}

	res.CPUUsage = clampFraction(cpuUsage(res.CPUCores))
	return res
}

// logicalCores prefers the OS count and falls back to the Go runtime's.
//
// runtime.NumCPU is the count this process may use, which is what matters for
// how much work the worker can take on, and it is never zero.
func logicalCores() int {
	if n, err := cpu.Counts(true); err == nil && n > 0 {
		return n
	}
	return runtime.NumCPU()
}

// cpuUsage is the busy share of the machine, 0..1.
//
// The fallback when the probe fails is goroutines per core rather than zero:
// zero would tell admission control the box is idle, which is the one answer
// that can make a struggling worker accept more work. It is a crude proxy and
// deliberately so — it predates this file and is kept for that reason.
func cpuUsage(cores int) float64 {
	if c, err := cpu.Percent(cpuSampleWindow, false); err == nil && len(c) > 0 {
		return c[0] / 100.0
	}
	if cores <= 0 {
		cores = 1
	}
	return float64(runtime.NumGoroutine()) / (float64(cores) * 100.0)
}

func clampFraction(v float64) float64 {
	if v < 0 || v != v { // NaN, which a JSON encoder refuses to marshal.
		return 0
	}
	return min(1.0, v)
}
