package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds configuration for the Engine.
type Config struct {
	MaxRetries          int           `json:"max_retries"`
	RetryInterval       time.Duration `json:"retry_interval"`
	ReconnectInterval   time.Duration `json:"reconnect_interval"`
	StatusInterval      time.Duration `json:"status_interval"`
	PrioritizeDLQ       bool          `json:"prioritize_dlq"`
	DryRun              bool          `json:"dry_run"`
	CheckpointInterval  time.Duration `json:"checkpoint_interval"`
	TraceSampleRate     float64       `json:"trace_sample_rate"` // 0.0 to 1.0
	AdaptiveThroughput  bool          `json:"adaptive_throughput"`
	MaxMemoryMB         uint64        `json:"max_memory_mb"`
	OutboxRelayInterval time.Duration `json:"outbox_relay_interval"`
	// MaxInflight bounds the number of messages processed concurrently across the pipeline.
	// Keep this conservative to limit memory usage. Defaults to 128.
	MaxInflight int `json:"max_inflight"`
	// DrainTimeout controls how long to wait for sink writers to drain on shutdown before logging a warning.
	// Does not forcibly terminate writers; set to 0 to wait indefinitely.
	DrainTimeout time.Duration `json:"drain_timeout"`
	// StallThreshold is how long the pipeline may hold outstanding work without
	// completing any of it before it is reported as stalled. A wedged pipeline
	// is otherwise indistinguishable from an idle one: it keeps reporting
	// "running" while delivering nothing. Set to 0 to use the default.
	StallThreshold time.Duration `json:"stall_threshold"`
	// LagWarnBytes is how much un-acknowledged WAL a source may retain before it
	// is reported. Retention accumulates on the source database, so this guards
	// someone else's disk, not Hermod's. Set to 0 to use the default.
	LagWarnBytes uint64 `json:"lag_warn_bytes"`
	// StreamSilenceInterval is how often a source's push stream is checked for
	// silence. The threshold itself belongs to the source, which derives it from
	// the server's keepalive cadence; this only controls how promptly the
	// silence is noticed. Set to 0 to use the default.
	StreamSilenceInterval time.Duration `json:"stream_silence_interval"`
}

// envDuration reads a positive duration from the environment, falling back to
// def for anything absent, unparseable or non-positive.
//
// The fallback is deliberately strict about non-positive values: zero switches
// stall detection off entirely, and a typo in a deployment variable must not
// quietly disable a safety mechanism.
func envDuration(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

// envBytes reads a positive byte count from the environment.
func envBytes(name string, def uint64) uint64 {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil || n == 0 {
		return def
	}
	return n
}

// DefaultConfig returns the default configuration for the Engine.
//
// The recovery thresholds are operational levers — an incident may call for
// detecting a stall sooner, and a workflow with a legitimately slow sink for
// detecting it later — so they are readable from the environment rather than
// fixed at build time.
func DefaultConfig() Config {
	return Config{
		MaxRetries:          3,
		RetryInterval:       100 * time.Millisecond,
		ReconnectInterval:   30 * time.Second,
		StatusInterval:      5 * time.Second,
		CheckpointInterval:  1 * time.Minute,
		OutboxRelayInterval: 1 * time.Minute,
		TraceSampleRate:     1.0,
		MaxInflight:         128,
		DrainTimeout:        10 * time.Second,
		StallThreshold:      envDuration("HERMOD_STALL_THRESHOLD", 60*time.Second),
		LagWarnBytes:        envBytes("HERMOD_LAG_WARN_BYTES", 256<<20),
		// Postgres keepalives arrive every wal_sender_timeout/2 (30s at the
		// 60s default), so a 10s sample notices silence promptly without
		// polling the source pointlessly often.
		StreamSilenceInterval: envDuration("HERMOD_STREAM_SILENCE_INTERVAL", 10*time.Second),
	}
}
