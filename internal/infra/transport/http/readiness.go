package http

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	dashboardhttp "github.com/gsoultan/Hermod/internal/dashboard/transport/http"

	"github.com/gsoultan/Hermod/internal/storage"
)

func (h *InfraHandler) HandleReadiness(w http.ResponseWriter, r *http.Request) {
	h.ReadyMu.Lock()
	defer h.ReadyMu.Unlock()

	ctx := r.Context()
	now := time.Now()

	// Debounce check
	debounceStr := os.Getenv("HERMOD_READY_DEBOUNCE")
	debounce := 0 * time.Second
	if debounceStr != "" {
		if d, err := time.ParseDuration(debounceStr); err == nil {
			debounce = d
		}
	}

	checks := make(map[string]any)
	if h.LastReadyStatusSet && time.Since(h.LastReadyStatusAt) < debounce {
		h.RespondReadiness(w, h.LastReadyStatus, statusFromBool(h.LastReadyStatus), checks)
		return
	}

	overallOK := true

	// 1. Database Check
	dbStart := time.Now()
	dbOK := true
	if h.Storage != nil {
		if _, _, err := h.Storage.ListSources(ctx, storage.CommonFilter{Limit: 1}); err != nil {
			dbOK = false
			overallOK = false
		}
	}
	checks["database"] = map[string]any{"ok": dbOK}
	recordReadiness("database", dbOK, dbStart)

	// 2. Workers Check
	workersStart := time.Now()
	workersOK := true
	recentWorkers := 0
	staleWorkers := 0
	ttl := 60
	if h.Storage != nil {
		allWorkers, _, err := h.Storage.ListWorkers(ctx, storage.CommonFilter{})
		if err == nil {
			for _, w := range allWorkers {
				if w.LastSeen != nil && time.Since(*w.LastSeen) < time.Duration(ttl)*time.Second {
					recentWorkers++
				} else {
					staleWorkers++
				}
			}
		} else {
			workersOK = false
			overallOK = false
		}
	}
	checks["workers"] = map[string]any{
		"ttl_seconds": ttl,
		"recent":      recentWorkers,
		"stale":       staleWorkers,
	}
	recordReadiness("workers", workersOK, workersStart)

	// 3. Leases Check
	leasesStart := time.Now()
	leasesOK := true
	activeOwned := 0
	totalActive := 0
	if h.Storage != nil {
		wfs, _, err := h.Storage.ListWorkflows(ctx, storage.CommonFilter{})
		if err == nil {
			for _, wf := range wfs {
				if wf.Active {
					totalActive++
					if wf.OwnerID != "" && wf.LeaseUntil != nil && wf.LeaseUntil.After(now) {
						activeOwned++
					} else {
						leasesOK = false
					}
				}
			}
		} else {
			leasesOK = false
			overallOK = false
		}
	}
	checks["leases"] = map[string]any{
		"ok":           leasesOK,
		"total":        totalActive,
		"active_owned": activeOwned,
	}
	recordReadiness("leases", leasesOK, leasesStart)

	if os.Getenv("HERMOD_READY_LEASES_REQUIRED") == "true" && !leasesOK {
		overallOK = false
	}

	h.LastReadyStatus = overallOK
	h.LastReadyStatusAt = now
	h.LastReadyStatusSet = true

	status := "ok"
	if !overallOK {
		status = "error"
	}

	h.RespondReadiness(w, overallOK, status, checks)
}

// recordReadiness publishes the result of one readiness check.
//
// hermod_readiness_status and hermod_readiness_latency_seconds were declared,
// documented and written by nothing. A gauge nobody writes is worse than a
// missing one: it appears in the metrics endpoint, an operator builds an alert
// on it, and the alert never fires because the series never changes.
//
// The label is per component because "the service is unready" is not
// actionable and "the database check failed" is. The latency is worth having on
// its own: a readiness check that has become slow is the warning before it
// starts failing.
func recordReadiness(component string, ok bool, started time.Time) {
	value := 0.0
	if ok {
		value = 1.0
	}
	dashboardhttp.ReadinessStatus.WithLabelValues(component).Set(value)
	dashboardhttp.ReadinessLatencySeconds.WithLabelValues(component).Observe(time.Since(started).Seconds())
}

func statusFromBool(ok bool) string {
	if ok {
		return "ok"
	}
	return "error"
}

func (h *InfraHandler) RespondReadiness(w http.ResponseWriter, ok bool, status string, checks map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	if !ok {
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	resp := map[string]any{
		"version": "v1",
		"status":  status,
		"time":    time.Now().UTC().Format(time.RFC3339Nano),
	}
	if checks != nil {
		resp["checks"] = checks
	}

	json.NewEncoder(w).Encode(resp)
}
