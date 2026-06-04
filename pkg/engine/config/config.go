package config

import "time"

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
}

// DefaultConfig returns the default configuration for the Engine.
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
	}
}
