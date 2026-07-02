package worker

import (
	"context"
	"hash/fnv"
	"math"
	"sort"
	"time"

	"github.com/user/hermod/internal/storage"
)

func (w *Worker) isAssigned(resourceID string, currentOwnerID string) bool {
	if w.workerGUID == "" {
		return w.isAssignedLegacy(resourceID)
	}
	return w.isAssignedResourceAware(resourceID, currentOwnerID)
}

func (w *Worker) isAssignedLegacy(resourceID string) bool {
	if w.totalWorkers <= 1 {
		return true
	}
	h := fnv.New32a()
	h.Write([]byte(resourceID))
	return int(h.Sum32())%w.totalWorkers == w.workerID
}

func (w *Worker) isAssignedResourceAware(resourceID string, currentOwnerID string) bool {
	w.refreshWorkerCache()

	w.cacheMu.RLock()
	online := w.workerCache
	w.cacheMu.RUnlock()

	if len(online) <= 1 {
		return true
	}

	bestID := w.calculateBestWorker(online, resourceID, currentOwnerID)
	return bestID == w.workerGUID
}

func (w *Worker) refreshWorkerCache() {
	ttl := w.workerCacheTTL
	if ttl == 0 {
		ttl = 10 * time.Second
	}
	w.cacheMu.RLock()
	stale := time.Since(w.workerCacheTime) > ttl
	w.cacheMu.RUnlock()

	if !stale {
		return
	}

	w.cacheMu.Lock()
	defer w.cacheMu.Unlock()
	if time.Since(w.workerCacheTime) <= ttl {
		return
	}

	ctx := context.Background()
	workers, _, err := w.storage.ListWorkers(ctx, storage.CommonFilter{})
	if err == nil {
		w.workerCache = w.filterOnlineWorkers(workers)
		w.workerCacheTime = time.Now()
	}
}

func (w *Worker) filterOnlineWorkers(workers []storage.Worker) []storage.Worker {
	var online []storage.Worker
	now := time.Now()
	threshold := w.onlineThreshold()
	selfPresent := false
	for _, wrk := range workers {
		if wrk.ID == w.workerGUID {
			// This worker knows it is alive, so always include itself with a
			// freshly-stamped entry (local CPU/mem, LastSeen=now) regardless of
			// how stale its persisted heartbeat is. Excluding self - or letting
			// a lagging heartbeat strip its freshness weight - would make the
			// worker stop all of its own workflows even though it is healthy.
			selfPresent = true
			online = append(online, w.selfWorkerEntry(now))
			continue
		}
		if wrk.LastSeen != nil && now.Sub(*wrk.LastSeen) < threshold {
			online = append(online, wrk)
		}
	}
	if !selfPresent && w.workerGUID != "" {
		online = append(online, w.selfWorkerEntry(now))
	}
	sort.Slice(online, func(i, j int) bool { return online[i].ID < online[j].ID })
	return online
}

// onlineThreshold is the window within which a peer's last heartbeat is
// considered alive for load-balancing. It is derived from the lease TTL so the
// online view stays consistent with lease expiry and stale-entry reclamation.
func (w *Worker) onlineThreshold() time.Duration {
	return time.Duration(max(1, w.leaseTTLSeconds)) * 3 * time.Second
}

// selfWorkerEntry synthesizes an online entry for this worker using its locally
// known resource usage, used when the persisted heartbeat is missing or stale.
func (w *Worker) selfWorkerEntry(now time.Time) storage.Worker {
	seen := now
	return storage.Worker{
		ID:          w.workerGUID,
		Name:        w.workerName,
		Host:        w.workerHost,
		Port:        w.workerPort,
		CPUUsage:    w.currentCPU,
		MemoryUsage: w.currentMem,
		LastSeen:    &seen,
	}
}

func (w *Worker) calculateBestWorker(online []storage.Worker, resourceID string, currentOwnerID string) string {
	var bestID string
	var maxScore = -1.0

	for _, wrk := range online {
		score := w.calculateScore(wrk, resourceID, currentOwnerID)
		if score > maxScore {
			maxScore = score
			bestID = wrk.ID
		}
	}
	return bestID
}

func (w *Worker) calculateScore(wrk storage.Worker, resourceID string, currentOwnerID string) float64 {
	h := fnv.New32a()
	h.Write([]byte(wrk.ID + ":" + resourceID))
	u := float64(h.Sum32()) / float64(0xFFFFFFFF)
	if u == 0 {
		u = 1e-9 // Avoid log(0)
	}

	weight := w.calculateWeight(wrk)
	if currentOwnerID != "" && wrk.ID == currentOwnerID {
		// Hysteresis bonus: substantially increase weight to prevent flapping
		weight *= 2.0
	}

	// Highest Random Weight (Rendezvous Hashing) with weights
	// score = log(u) / weight. Since log(u) <= 0, the node with the largest weight
	// (least loaded) will have a score closest to 0 (the maximum score).
	return math.Log(u) / weight
}

func (w *Worker) calculateWeight(wrk storage.Worker) float64 {
	// Base weight is 1.0. Decrease as load increases.
	cpuFactor := math.Max(0.1, 1.0-wrk.CPUUsage)
	memFactor := math.Max(0.1, 1.0-wrk.MemoryUsage)
	weight := cpuFactor * memFactor

	if wrk.LastSeen != nil {
		lastSeen := time.Since(*wrk.LastSeen)
		if lastSeen > 60*time.Second {
			// Deprioritize workers that haven't heartbeated recently
			weight *= 0.1
		}
	}
	return weight
}
