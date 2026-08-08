package worker

import (
	"os"
	"strconv"
	"strings"
)

// Admission control refuses to start *new* workflows while the host is loaded,
// so a worker under pressure sheds work instead of collapsing. Two things about
// it need to be operator-controlled rather than baked in:
//
//  1. The reading is host-wide (gopsutil reports the whole machine, see
//     health.go), not this process's share. On a dedicated worker node that is
//     exactly right — the CPU really is gone. On a shared host it is not:
//     Hermod refuses to start workflows because of load it does not own, and
//     says so only in a log line.
//
//  2. The threshold itself is a capacity decision. A worker sized for bursty
//     CDC traffic wants more headroom than one running a few polling sources.
//
// Defaults keep the historical 0.85 behaviour, so nothing changes unless it is
// asked for. Setting a threshold to 1 or above disables that dimension, which
// is the escape hatch for a shared host where the host-wide reading is
// meaningless.
const (
	defaultAdmissionCPUThreshold = 0.85
	defaultAdmissionMemThreshold = 0.85
)

var (
	admissionCPUThreshold = envFloat("HERMOD_ADMISSION_CPU_THRESHOLD", defaultAdmissionCPUThreshold)
	admissionMemThreshold = envFloat("HERMOD_ADMISSION_MEM_THRESHOLD", defaultAdmissionMemThreshold)

	// leaseHysteresisFactor is the weight multiplier a workflow's current owner
	// receives when workers compete for it. See calculateScore in sharding.go
	// for what it buys and what it costs. 1.0 disables it entirely.
	leaseHysteresisFactor = envFloat("HERMOD_LEASE_HYSTERESIS", defaultLeaseHysteresis)
)

// defaultLeaseHysteresis preserves the historical 2x bonus.
const defaultLeaseHysteresis = 2.0

// envFloat reads a float from the environment, falling back to def when the
// variable is unset or unparseable. A malformed value falls back rather than
// failing startup: refusing to boot a worker over a typo in a tuning knob
// trades a small misconfiguration for an outage.
func envFloat(key string, def float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 {
		return def
	}
	return v
}
