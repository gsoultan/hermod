// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package telemetry

import "time"

// StatusUpdate represents the state of an engine at a point in time.
type StatusUpdate struct {
	WorkflowID       string            `json:"workflow_id,omitempty"`
	EngineStatus     string            `json:"engine_status,omitempty"`
	SourceStatus     string            `json:"source_status,omitempty"`
	SourceID         string            `json:"source_id,omitempty"`
	SinkStatuses     map[string]string `json:"sink_statuses,omitempty"`
	SinkID           string            `json:"sink_id,omitempty"`
	SinkStatus       string            `json:"sink_status,omitempty"`
	ProcessedCount   uint64            `json:"processed_count"`
	DeadLetterCount  uint64            `json:"dead_letter_count,omitempty"`
	NodeMetrics      map[string]uint64 `json:"node_metrics,omitempty"`
	NodeErrorMetrics map[string]uint64 `json:"node_error_metrics,omitempty"`
	NodeSamples      map[string]any    `json:"node_samples,omitempty"`
	EdgeMetrics      map[string]uint64 `json:"edge_metrics,omitempty"`
	// EdgeCounts is each canvas edge's cumulative message count, keyed by the
	// edge's ID. EdgeMetrics is keyed by the pair of nodes an edge joins, so
	// two edges between the same nodes cannot be told apart there.
	EdgeCounts map[string]uint64 `json:"edge_counts,omitempty"`
	// SampledAt is when EdgeCounts was read, in Unix milliseconds. Frames go
	// out at uneven intervals, so a rate is the count delta over this delta.
	SampledAt int64 `json:"sampled_at,omitempty"`
	// NodeLatencies is each node's moving-average run time, in nanoseconds
	// like AvgLatency. Nodes that have not run are absent.
	NodeLatencies map[string]time.Duration `json:"node_latencies,omitempty"`
	// NodeBusy lists each node with a run in flight and how long since it last
	// finished one. A node that never returns stops its counters just as an
	// idle node does; this is what tells the two apart.
	NodeBusy map[string]NodeBusy `json:"node_busy,omitempty"`
	// NodeHeld is how many messages the debugger is holding before each node.
	NodeHeld         map[string]int64   `json:"node_held,omitempty"`
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
