package telemetry

import "github.com/prometheus/client_golang/prometheus"

// labelDeleter is the subset of a Prometheus *Vec that can drop series by label.
type labelDeleter interface {
	DeletePartialMatch(prometheus.Labels) int
}

// workflowLabelled lists every metric carrying a workflow_id label.
//
// Prometheus never reclaims a series on its own: once a workflow id has been
// observed, its series stay in the registry for the life of the process, and
// every scrape keeps exporting them. Deleting a workflow therefore left its
// metrics behind permanently, and a platform where pipelines are created and
// removed regularly leaks memory and scrape payload without bound. Measured on
// the churn soak: 6,522 distinct workflow ids took the heap from 23 MiB to
// 50 MiB, while the same run against a single stable id stayed at 9.6 -> 9.8 MiB.
//
// Keep this list in step with metrics.go — a metric added there with a
// workflow_id label and not added here simply never gets cleaned up.
// TestForgetWorkflowCoversEveryWorkflowLabelledMetric fails when they diverge.
func workflowLabelled() []labelDeleter {
	return []labelDeleter{
		BackpressureDropTotal,
		BackpressureSpillTotal,
		DeadLetterCount,
		DeadLetterErrors,
		IdempotencyConflictsTotal,
		IdempotencyDedupTotal,
		IdempotencyKeysTotal,
		IdempotencyLatency,
		IdempotencyMissingTotal,
		MessageErrors,
		MessagesDroppedNoTarget,
		MessagesProcessed,
		PostgresSlotLag,
		ProcessingLatency,
		SinkWriteCount,
		SinkWriteErrors,
		SubSourceBackoff,
		WorkflowNodeErrors,
		WorkflowNodeProcessed,
	}
}

// ForgetWorkflow drops every metric series belonging to a workflow.
//
// Call it when a workflow is deleted permanently — not when it is merely
// stopped or paused, where the counters should survive so the history of a
// restarted pipeline stays intact.
//
// Returns the number of series removed, which is useful in tests and harmless
// to ignore.
func ForgetWorkflow(workflowID string) int {
	if workflowID == "" {
		return 0
	}
	match := prometheus.Labels{"workflow_id": workflowID}
	removed := 0
	for _, vec := range workflowLabelled() {
		removed += vec.DeletePartialMatch(match)
	}
	return removed
}
