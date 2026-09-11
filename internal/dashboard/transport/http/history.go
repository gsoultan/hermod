package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

const (
	// defaultHistoryWindow is what the dashboard chart asks for on load.
	defaultHistoryWindow = time.Hour

	// maxHistoryWindow matches the *default* retention sweep in the registry.
	// A longer window cannot return anything extra, so clamping to it bounds
	// the scan without ever costing a caller a row.
	//
	// A deployment that shortens retention via HERMOD_DASHBOARD_HISTORY_RETENTION
	// is not clamped any tighter here, and does not need to be: the rows are
	// already gone, so the shorter window is enforced by the sweep rather than
	// by this ceiling.
	maxHistoryWindow = 7 * 24 * time.Hour

	// defaultHistoryLimit is an hour of five-second samples with room to
	// spare, so the default window returns whole under the default limit.
	defaultHistoryLimit = 720

	// maxHistoryLimit bounds the response. The window and the limit are both
	// caller-supplied and both reach a query, so neither is allowed to be the
	// one that is unbounded.
	maxHistoryLimit = 5000
)

// parseHistoryWindow turns the query string into a cutoff and a row limit.
//
// Malformed input falls back to the defaults rather than returning 400. This
// is a chart on a dashboard: refusing to render one because a stale bookmark
// carries window=1hour instead of 1h tells the reader the system is broken
// when it is not.
func parseHistoryWindow(window, limit string) (time.Time, int) {
	w := defaultHistoryWindow
	if parsed, err := time.ParseDuration(window); err == nil && parsed > 0 {
		w = min(parsed, maxHistoryWindow)
	}

	n := defaultHistoryLimit
	if parsed, err := strconv.Atoi(limit); err == nil && parsed > 0 {
		n = min(parsed, maxHistoryLimit)
	}

	return time.Now().Add(-w), n
}

// GetDashboardHistory serves the persisted metric series behind the chart.
func (h *DashboardHandler) GetDashboardHistory(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	since, limit := parseHistoryWindow(query.Get("window"), query.Get("limit"))

	samples, err := h.Registry.GetDashboardHistory(r.Context(), query.Get("vhost"), since, limit)
	if err != nil {
		h.JsonError(w, "Failed to get dashboard history: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Encode an empty series as [] rather than null: a chart that has to
	// special-case null before it can check length is a chart that eventually
	// forgets to.
	if samples == nil {
		samples = []storage.DashboardSample{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(samples)
}
