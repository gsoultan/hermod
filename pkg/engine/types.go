package engine

import (
	"context"
	"time"

	"github.com/user/hermod"
)

type RoutedMessage struct {
	SinkIndex int
	Message   hermod.Message
}

type RouterFunc func(ctx context.Context, msg hermod.Message) ([]RoutedMessage, error)

type StatusUpdate struct {
	WorkflowID       string             `json:"workflow_id,omitempty"`
	EngineStatus     string             `json:"engine_status,omitempty"`
	SourceStatus     string             `json:"source_status,omitempty"`
	SourceID         string             `json:"source_id,omitempty"`
	SinkStatuses     map[string]string  `json:"sink_statuses,omitempty"`
	SinkID           string             `json:"sink_id,omitempty"`
	SinkStatus       string             `json:"sink_status,omitempty"`
	ProcessedCount   uint64             `json:"processed_count"`
	DeadLetterCount  uint64             `json:"dead_letter_count,omitempty"`
	NodeMetrics      map[string]uint64  `json:"node_metrics,omitempty"`
	NodeErrorMetrics map[string]uint64  `json:"node_error_metrics,omitempty"`
	NodeSamples      map[string]any     `json:"node_samples,omitempty"`
	EdgeMetrics      map[string]uint64  `json:"edge_metrics,omitempty"`
	SinkCBStatuses   map[string]string  `json:"sink_cb_statuses,omitempty"`
	SinkBufferFill   map[string]float64 `json:"sink_buffer_fill,omitempty"`
	AverageDQScore   float64            `json:"average_dq_score,omitempty"`
	AvgLatency       time.Duration      `json:"avg_latency,omitempty"`
	PendingApprovals map[string]uint64  `json:"pending_approvals,omitempty"`
}

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

type BackpressureStrategy string

const (
	BPBlock       BackpressureStrategy = "block"
	BPDropOldest  BackpressureStrategy = "drop_oldest"
	BPDropNewest  BackpressureStrategy = "drop_newest"
	BPSampling    BackpressureStrategy = "sampling"
	BPSpillToDisk BackpressureStrategy = "spill_to_disk"
)

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
	CircuitBreakerCoolDown  time.Duration `json:"cb_cool_off"` // match internal/engine/registry.go key "circuit_cool_off"

	// Backpressure settings
	BackpressureStrategy BackpressureStrategy `json:"backpressure_strategy"`
	BackpressureBuffer   int                  `json:"backpressure_buffer"`
	SamplingRate         float64              `json:"sampling_rate"` // 0.0 to 1.0

	// Spill to Disk settings
	SpillPath    string `json:"spill_path"`
	SpillMaxSize int    `json:"spill_max_size"`
}

type SourceConfig struct {
	ReconnectIntervals []time.Duration `json:"reconnect_intervals"`
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
