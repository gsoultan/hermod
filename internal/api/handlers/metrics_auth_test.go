package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ---------------------------------------------------------------------------
// /metrics exposure.
//
// The endpoint is in the authentication bypass list, which is the right default
// for a Prometheus scrape target: the usual answer is to keep it on a private
// network, and requiring a session cookie would break every scraper.
//
// But "keep it private" is an assumption about someone else's network, and
// Hermod's metrics are not neutral. They carry workflow_id, source_id and
// worker_id labels, so anyone who can reach the port learns the shape of the
// deployment — how many pipelines, named, and which are failing.
//
// So the default stays open and a token makes it closed, rather than the other
// way round: setting HERMOD_METRICS_TOKEN is a deliberate act, and an operator
// who does not set it is in exactly the position they were in before.
// ---------------------------------------------------------------------------

func metricsRequest(token string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

// guarded wraps a handler the way the server does, so these exercise the real
// middleware rather than a reimplementation of it.
func guarded(t *testing.T, h *Handler) http.Handler {
	t.Helper()
	return h.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hermod_engine_active_total 1"))
	}))
}

// TestMetricsOpenByDefault pins the existing behaviour. Turning this off by
// default would silently break every deployment's scraping on upgrade.
func TestMetricsOpenByDefault(t *testing.T) {
	t.Setenv("HERMOD_METRICS_TOKEN", "")

	rec := httptest.NewRecorder()
	guarded(t, &Handler{}).ServeHTTP(rec, metricsRequest(""))

	if rec.Code != http.StatusOK {
		t.Errorf("/metrics returned %d with no token configured, want 200: scraping "+
			"must keep working for anyone who has not opted in", rec.Code)
	}
}

// TestMetricsRequiresTokenWhenConfigured is the opt-in.
func TestMetricsRequiresTokenWhenConfigured(t *testing.T) {
	t.Setenv("HERMOD_METRICS_TOKEN", "s3cret-scrape-token")

	for _, tc := range []struct {
		name  string
		token string
		want  int
	}{
		{"no credentials", "", http.StatusUnauthorized},
		{"wrong token", "not-the-token", http.StatusUnauthorized},
		{"correct token", "s3cret-scrape-token", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			guarded(t, &Handler{}).ServeHTTP(rec, metricsRequest(tc.token))
			if rec.Code != tc.want {
				t.Errorf("/metrics with %s returned %d, want %d; the metrics carry "+
					"workflow and worker names, so an unauthenticated read maps the deployment",
					tc.name, rec.Code, tc.want)
			}
		})
	}
}

// TestHealthProbesStayOpenWhenMetricsIsGuarded is the mistake worth guarding
// against: a token that also covered /livez and /readyz would make the kubelet
// fail every probe and restart the pod in a loop.
func TestHealthProbesStayOpenWhenMetricsIsGuarded(t *testing.T) {
	t.Setenv("HERMOD_METRICS_TOKEN", "s3cret-scrape-token")

	for _, path := range []string{"/livez", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			guarded(t, &Handler{}).ServeHTTP(rec, r)
			if rec.Code != http.StatusOK {
				t.Errorf("%s returned %d while a metrics token was set; Kubernetes would "+
					"fail the probe and restart the pod on a loop", path, rec.Code)
			}
		})
	}
}
