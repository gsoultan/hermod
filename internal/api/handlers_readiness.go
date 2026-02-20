package api

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/user/hermod/internal/storage"
)

func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	s.readyMu.Lock()
	defer s.readyMu.Unlock()

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
	if s.lastReadyStatusSet && now.Sub(s.lastReadyStatusAt) < debounce {
		s.respondReadiness(w, s.lastReadyStatus, statusFromBool(s.lastReadyStatus), checks)
		return
	}

	overallOK := true

	// 1. Database Check
	dbOK := true
	if s.storage != nil {
		if _, _, err := s.storage.ListSources(ctx, storage.CommonFilter{Limit: 1}); err != nil {
			dbOK = false
			overallOK = false
		}
	}
	checks["database"] = map[string]any{"ok": dbOK}

	// 2. Workers Check
	recentWorkers := 0
	staleWorkers := 0
	ttl := 60
	if s.storage != nil {
		allWorkers, _, err := s.storage.ListWorkers(ctx, storage.CommonFilter{})
		if err == nil {
			for _, w := range allWorkers {
				if w.LastSeen != nil && now.Sub(*w.LastSeen) < time.Duration(ttl)*time.Second {
					recentWorkers++
				} else {
					staleWorkers++
				}
			}
		} else {
			overallOK = false
		}
	}
	checks["workers"] = map[string]any{
		"ttl_seconds": ttl,
		"recent":      recentWorkers,
		"stale":       staleWorkers,
	}

	// 3. Leases Check
	leasesOK := true
	activeOwned := 0
	totalActive := 0
	if s.storage != nil {
		wfs, _, err := s.storage.ListWorkflows(ctx, storage.CommonFilter{})
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

	if os.Getenv("HERMOD_READY_LEASES_REQUIRED") == "true" && !leasesOK {
		overallOK = false
	}

	s.lastReadyStatus = overallOK
	s.lastReadyStatusAt = now
	s.lastReadyStatusSet = true

	status := "ok"
	if !overallOK {
		status = "error"
	}

	s.respondReadiness(w, overallOK, status, checks)
}

func statusFromBool(ok bool) string {
	if ok {
		return "ok"
	}
	return "error"
}

func (s *Server) respondReadiness(w http.ResponseWriter, ok bool, status string, checks map[string]any) {
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
