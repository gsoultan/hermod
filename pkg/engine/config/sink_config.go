package config

import "time"

// SinkConfig holds configuration for a specific sink.
type SinkConfig struct {
	MaxRetries     int             `json:"max_retries"`
	RetryInterval  time.Duration   `json:"retry_interval"`
	RetryIntervals []time.Duration `json:"retry_intervals"`
	BatchSize      int             `json:"batch_size"`
	BatchTimeout   time.Duration   `json:"batch_timeout"`
	// BatchBytes triggers a flush when the accumulated payload size reaches this threshold.
	// Set to 0 to disable byte-based flushing.
	BatchBytes       int  `json:"batch_bytes"`
	AdaptiveBatching bool `json:"adaptive_batching"`
	Concurrency      int  `json:"concurrency"`

	// Per-key sharding for ordered concurrency
	ShardCount   int    `json:"shard_count"`
	ShardKeyMeta string `json:"shard_key_meta"` // when empty, use Message.ID()

	// Circuit Breaker settings
	CircuitBreakerThreshold int           `json:"cb_threshold"`
	CircuitBreakerInterval  time.Duration `json:"cb_interval"`
	CircuitBreakerCoolDown  time.Duration `json:"cb_cool_off"`

	// Backpressure settings
	BackpressureStrategy BackpressureStrategy `json:"backpressure_strategy"`
	BackpressureBuffer   int                  `json:"backpressure_buffer"`
	SamplingRate         float64              `json:"sampling_rate"` // 0.0 to 1.0

	// Spill to Disk settings
	SpillPath    string `json:"spill_path"`
	SpillMaxSize int    `json:"spill_max_size"`
}
