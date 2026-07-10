package telemetry

import "time"

// StatusUpdate represents the state of an engine at a point in time.
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
	Throughput       float64            `json:"throughput,omitempty"`
	Lag              uint64             `json:"lag,omitempty"`
	ErrorRate        float64            `json:"error_rate,omitempty"`
	Backpressure     float64            `json:"backpressure,omitempty"`
	PendingApprovals map[string]uint64  `json:"pending_approvals,omitempty"`
}
