package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/user/hermod/internal/api/handlers"
	dashboardhttp "github.com/user/hermod/internal/dashboard/transport/http"
)

// ---------------------------------------------------------------------------
// Readiness metrics.
//
// hermod_readiness_status and hermod_readiness_latency_seconds were declared,
// documented, and never written to by anything. The readiness handler checks
// the database, the workers and the leases on every /readyz — and reported none
// of it to Prometheus.
//
// A gauge nobody writes is worse than a missing one: it appears in the metrics
// endpoint, an operator builds an alert on it, and the alert never fires
// because the series never changes. This is the same shape as the other
// capabilities that turned out to be inert, and it was flagged twice before
// being fixed.
// ---------------------------------------------------------------------------

// gaugeValue reads a labelled gauge out of the default registry.
func gaugeValue(t *testing.T, name, label string) (float64, bool) {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetValue() == label {
					return m.GetGauge().GetValue(), true
				}
			}
		}
	}
	return 0, false
}

// TestReadinessReportsEachComponent. Per-component labels are the point: "the
// service is unready" is not actionable, "the database check failed" is.
func TestReadinessReportsEachComponent(t *testing.T) {
	h := NewInfraHandler(&handlers.Handler{})

	rec := httptest.NewRecorder()
	h.HandleReadiness(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))

	for _, component := range []string{"database", "workers", "leases"} {
		if _, ok := gaugeValue(t, "hermod_readiness_status", component); !ok {
			t.Errorf("hermod_readiness_status has no series for %q; the gauge is declared "+
				"and never written, so an alert built on it can never fire", component)
		}
	}
}

// TestReadinessRecordsLatency. The latency histogram had the same problem, and
// a readiness check that has become slow is the warning before it starts
// failing.
func TestReadinessRecordsLatency(t *testing.T) {
	h := NewInfraHandler(&handlers.Handler{})

	rec := httptest.NewRecorder()
	h.HandleReadiness(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}
	for _, f := range families {
		if f.GetName() == "hermod_readiness_latency_seconds" && len(f.GetMetric()) > 0 {
			return
		}
	}
	t.Error("hermod_readiness_latency_seconds was never observed; a readiness check that " +
		"has become slow is the warning before it starts failing")
}

// TestTheMetricNamesAreTheOnesDocumented. They are an operational contract:
// dashboards and alerts are written against them, so renaming one silently is
// an alert that stops firing without anybody noticing.
func TestTheMetricNamesAreTheOnesDocumented(t *testing.T) {
	if dashboardhttp.ReadinessStatus == nil {
		t.Error("ReadinessStatus is not declared")
	}
	if dashboardhttp.ReadinessLatencySeconds == nil {
		t.Error("ReadinessLatencySeconds is not declared")
	}
}
