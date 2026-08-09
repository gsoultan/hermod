package config

import (
	"testing"
	"time"
)

// TestShutdownBudgetStagesNest is the invariant the old scattered timeouts
// violated: an inner stage must always finish inside the stage that contains
// it. When they were independent constants (60s worker cleanup, 45s StopAll,
// 35s workflow stop, 10s drain) a stage could be cut off by a parent deadline
// it had no knowledge of — halfway through the drain that exists to avoid
// losing data.
func TestShutdownBudgetStagesNest(t *testing.T) {
	totals := []time.Duration{
		1 * time.Second,
		5 * time.Second,
		25 * time.Second,
		30 * time.Second,
		2 * time.Minute,
		10 * time.Minute,
	}

	for _, total := range totals {
		t.Run(total.String(), func(t *testing.T) {
			b := budgetFrom(total)

			if b.PerEngine > b.Total {
				t.Errorf("PerEngine %v exceeds Total %v: stopping one workflow can outlive the whole shutdown",
					b.PerEngine, b.Total)
			}
			if b.Drain > b.PerEngine {
				t.Errorf("Drain %v exceeds PerEngine %v: the drain can be cut off by its own parent",
					b.Drain, b.PerEngine)
			}
			if b.Drain+b.Grace > b.PerEngine {
				t.Errorf("Drain+Grace (%v) exceeds PerEngine %v: writers are killed before they finish unwinding",
					b.Drain+b.Grace, b.PerEngine)
			}
			if b.Total <= 0 || b.PerEngine <= 0 || b.Drain <= 0 || b.Grace <= 0 {
				t.Errorf("non-positive stage in %+v", b)
			}
		})
	}
}

// TestShutdownDefaultFitsKubernetesGracePeriod pins the reason for the default.
// Kubernetes SIGKILLs at terminationGracePeriodSeconds, 30s unless overridden.
// A total at or above that is killed rather than allowed to finish, which
// discards whatever the drain had not yet delivered.
func TestShutdownDefaultFitsKubernetesGracePeriod(t *testing.T) {
	const k8sDefaultGrace = 30 * time.Second

	b := budgetFrom(defaultShutdownTotal)
	if b.Total >= k8sDefaultGrace {
		t.Errorf("default shutdown total %v is not below Kubernetes' default grace period %v; "+
			"a rolling deploy would SIGKILL the process mid-drain", b.Total, k8sDefaultGrace)
	}
	// Margin matters: the process still has to close storage after the engines
	// stop, and the kubelet's timer starts before the signal is delivered.
	if margin := k8sDefaultGrace - b.Total; margin < 3*time.Second {
		t.Errorf("only %v of margin below the default grace period; too tight to absorb "+
			"storage close and signal delivery", margin)
	}
}

// TestShutdownHonoursTheEnvironmentOverride covers the escape hatch for
// deployments that have raised their grace period.
func TestShutdownHonoursTheEnvironmentOverride(t *testing.T) {
	t.Setenv("HERMOD_SHUTDOWN_TIMEOUT", "90s")
	if got := Shutdown().Total; got != 90*time.Second {
		t.Errorf("Shutdown().Total = %v, want 90s", got)
	}

	// A malformed value must not stop a worker from booting.
	t.Setenv("HERMOD_SHUTDOWN_TIMEOUT", "not-a-duration")
	if got := Shutdown().Total; got != defaultShutdownTotal {
		t.Errorf("garbage override gave %v, want the default %v", got, defaultShutdownTotal)
	}

	t.Setenv("HERMOD_SHUTDOWN_TIMEOUT", "-5s")
	if got := Shutdown().Total; got != defaultShutdownTotal {
		t.Errorf("negative override gave %v, want the default %v", got, defaultShutdownTotal)
	}
}

// TestClampDrainKeepsPerSinkSettingsInsideTheBudget covers the user-facing
// DrainTimeout, which predates this budget and can be set larger than the whole
// shutdown.
func TestClampDrainKeepsPerSinkSettingsInsideTheBudget(t *testing.T) {
	b := budgetFrom(25 * time.Second)

	for _, tc := range []struct {
		name       string
		configured time.Duration
		want       time.Duration
	}{
		{"unset falls back to the budget", 0, b.Drain},
		{"negative falls back to the budget", -1, b.Drain},
		{"smaller is honoured", time.Second, time.Second},
		{"larger is clamped", time.Hour, b.Drain},
		{"exactly the budget is honoured", b.Drain, b.Drain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := b.ClampDrain(tc.configured); got != tc.want {
				t.Errorf("ClampDrain(%v) = %v, want %v", tc.configured, got, tc.want)
			}
		})
	}
}
