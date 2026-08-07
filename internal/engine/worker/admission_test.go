package worker

import (
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/user/hermod/pkg/engine/telemetry"
)

// counterValue reads a labelled counter's current value.
func counterValue(t *testing.T, workerID, reason string) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := telemetry.WorkerAdmissionRejected.WithLabelValues(workerID, reason).Write(m); err != nil {
		t.Fatalf("reading admission counter: %v", err)
	}
	return m.GetCounter().GetValue()
}

// TestAdmissionRejectionIsCounted covers the gap that made this failure mode
// dangerous: a worker over its threshold declines to start workflows and says
// so only in a log line, so from the outside a shedding platform and an idle
// one look identical. The rejection has to be countable.
func TestAdmissionRejectionIsCounted(t *testing.T) {
	store := newHardeningStorage(1)
	w, reg := newHardenedWorker(store, "worker-admission")
	t.Cleanup(func() { reg.StopAll(); reg.Close() })

	ctx := t.Context()
	if err := w.SelfRegister(ctx); err != nil {
		t.Fatalf("SelfRegister: %v", err)
	}

	// Well over the default 0.85 threshold on CPU.
	w.SetMetrics(0.99, 0.10)
	before := counterValue(t, "worker-admission", "cpu")

	w.sync(ctx, false)

	if got := counterValue(t, "worker-admission", "cpu"); got <= before {
		t.Errorf("admission rejection was not counted: %v -> %v", before, got)
	}
	if n := runningWorkflows(reg, store); n != 0 {
		t.Errorf("%d workflows started while over the CPU threshold", n)
	}

	// Below the threshold it must start normally — the shed is conditional, not
	// a latch.
	w.SetMetrics(0.05, 0.05)
	if !waitCond(15*time.Second, func() bool {
		w.SetMetrics(0.05, 0.05)
		w.sync(ctx, false)
		return runningWorkflows(reg, store) == 1
	}) {
		t.Errorf("workflow did not start after load dropped below the threshold")
	}
}

// TestAdmissionThresholdsAreConfigurable proves the knob exists and is honoured.
// The reading behind it is host-wide, so on a shared machine the default 0.85
// can refuse work over load Hermod does not own; raising the threshold is the
// documented escape hatch and must actually work.
func TestAdmissionThresholdsAreConfigurable(t *testing.T) {
	origCPU, origMem := admissionCPUThreshold, admissionMemThreshold
	t.Cleanup(func() { admissionCPUThreshold, admissionMemThreshold = origCPU, origMem })

	// Raise the ceiling above any possible reading: nothing may be shed.
	admissionCPUThreshold, admissionMemThreshold = 2.0, 2.0

	store := newHardeningStorage(2)
	w, reg := newHardenedWorker(store, "worker-admission-cfg")
	t.Cleanup(func() { reg.StopAll(); reg.Close() })

	ctx := t.Context()
	if err := w.SelfRegister(ctx); err != nil {
		t.Fatalf("SelfRegister: %v", err)
	}
	w.SetMetrics(0.99, 0.99) // would be rejected at the default threshold

	if !waitCond(15*time.Second, func() bool {
		w.SetMetrics(0.99, 0.99)
		w.sync(ctx, false)
		return runningWorkflows(reg, store) == 2
	}) {
		t.Errorf("raising the admission threshold did not allow the workflows to start: %d/2 running",
			runningWorkflows(reg, store))
	}
}

// TestEnvFloatFallsBackOnGarbage locks in the startup-safety choice: a typo in a
// tuning knob must not stop a worker from booting.
func TestEnvFloatFallsBackOnGarbage(t *testing.T) {
	const key = "HERMOD_TEST_ADMISSION_FLOAT"
	for _, tc := range []struct {
		name, value string
		want        float64
	}{
		{"unset", "", 0.85},
		{"garbage", "not-a-number", 0.85},
		{"negative", "-1", 0.85},
		{"zero", "0", 0.85},
		{"valid", "0.5", 0.5},
		{"padded", "  0.7  ", 0.7},
		{"above-one-disables", "2", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.value != "" {
				t.Setenv(key, tc.value)
			}
			if got := envFloat(key, 0.85); got != tc.want {
				t.Errorf("envFloat(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
