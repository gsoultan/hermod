package worker

import (
	"context"
	"hash/fnv"
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
	for _, wrk := range workers {
		if wrk.LastSeen != nil && now.Sub(*wrk.LastSeen) < 90*time.Second {
			online = append(online, wrk)
		}
	}
	sort.Slice(online, func(i, j int) bool { return online[i].ID < online[j].ID })
	return online
}

func (w *Worker) calculateBestWorker(online []storage.Worker, resourceID string, currentOwnerID string) string {
	var bestID string
	var maxScore float64 = -1.0

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

	weight := w.calculateWeight(wrk)
	if currentOwnerID != "" && wrk.ID == currentOwnerID {
		weight *= 10.0 // Hysteresis bonus for current owner
	}

	return float64(h.Sum32()) * weight
}

func (w *Worker) calculateWeight(wrk storage.Worker) float64 {
	cpuWeight := max(0.05, 1.1-wrk.CPUUsage)
	memWeight := max(0.05, 1.1-wrk.MemoryUsage)
	weight := cpuWeight * memWeight

	if wrk.LastSeen != nil {
		lastSeen := time.Since(*wrk.LastSeen)
		if lastSeen < 30*time.Second {
			weight *= 2.0
		} else if lastSeen < 60*time.Second {
			weight *= 1.2
		}
	}
	return weight
}
