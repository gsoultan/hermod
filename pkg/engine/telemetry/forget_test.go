package telemetry

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// seriesFor counts how many series in the default registry carry the given
// workflow_id.
func seriesFor(t *testing.T, workflowID string) int {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}
	n := 0
	for _, f := range families {
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "workflow_id" && l.GetValue() == workflowID {
					n++
				}
			}
		}
	}
	return n
}

// TestForgetWorkflowRemovesEverySeries is the core behaviour: after a workflow
// is deleted, nothing of it should remain in the registry.
func TestForgetWorkflowRemovesEverySeries(t *testing.T) {
	const id = "wf-forget-me"

	// Touch every workflow-labelled metric so each has a live series.
	MessagesProcessed.WithLabelValues(id, "src").Inc()
	MessageErrors.WithLabelValues(id, "src", "sink").Inc()
	SinkWriteCount.WithLabelValues(id, "snk").Inc()
	SinkWriteErrors.WithLabelValues(id, "snk").Inc()
	MessagesDroppedNoTarget.WithLabelValues(id).Inc()
	ProcessingLatency.WithLabelValues(id).Observe(0.1)
	DeadLetterCount.WithLabelValues(id, "snk").Inc()
	WorkflowNodeProcessed.WithLabelValues(id, "n1", "transformation").Inc()
	WorkflowNodeErrors.WithLabelValues(id, "n1", "transformation").Inc()
	SubSourceBackoff.WithLabelValues(id, "s1").Inc()
	PostgresSlotLag.WithLabelValues(id, "slot").Set(1)
	IdempotencyKeysTotal.WithLabelValues(id).Inc()

	if before := seriesFor(t, id); before == 0 {
		t.Fatal("no series were created; the test cannot prove anything")
	}

	// A different workflow must be left alone.
	const other = "wf-keep-me"
	MessagesProcessed.WithLabelValues(other, "src").Inc()
	otherBefore := seriesFor(t, other)

	ForgetWorkflow(id)

	if after := seriesFor(t, id); after != 0 {
		t.Errorf("%d series still carry workflow_id=%q after ForgetWorkflow; "+
			"Prometheus never reclaims these on its own, so they leak for the life of the process", after, id)
	}
	if after := seriesFor(t, other); after != otherBefore {
		t.Errorf("ForgetWorkflow(%q) also removed series for %q: %d -> %d", id, other, otherBefore, after)
	}
}

// TestForgetWorkflowIgnoresEmptyID guards against a stray call wiping metrics
// for every workflow at once — DeletePartialMatch on an empty label set would
// match everything.
func TestForgetWorkflowIgnoresEmptyID(t *testing.T) {
	const id = "wf-survivor"
	MessagesProcessed.WithLabelValues(id, "src").Inc()
	before := seriesFor(t, id)

	if removed := ForgetWorkflow(""); removed != 0 {
		t.Errorf("ForgetWorkflow(\"\") removed %d series; an empty id must be a no-op", removed)
	}
	if after := seriesFor(t, id); after != before {
		t.Errorf("ForgetWorkflow(\"\") disturbed an unrelated workflow: %d -> %d", before, after)
	}
}

// TestForgetWorkflowCoversEveryWorkflowLabelledMetric keeps the list honest.
//
// A metric declared in metrics.go with a workflow_id label but missing from
// workflowLabelled() is simply never cleaned up — the leak comes back silently
// for that one metric. Rather than trusting a comment, read the declarations.
func TestForgetWorkflowCoversEveryWorkflowLabelledMetric(t *testing.T) {
	f, err := os.Open("metrics.go")
	if err != nil {
		t.Fatalf("opening metrics.go: %v", err)
	}
	defer f.Close()

	declRe := regexp.MustCompile(`^\s*([A-Z]\w*)\s*=\s*promauto\.New`)
	var current string
	declared := map[string]bool{}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := declRe.FindStringSubmatch(line); m != nil {
			current = m[1]
		}
		if current != "" && strings.Contains(line, `"workflow_id"`) {
			declared[current] = true
			current = ""
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scanning metrics.go: %v", err)
	}
	if len(declared) == 0 {
		t.Fatal("found no workflow_id-labelled metrics; the scanner is broken")
	}

	covered := map[string]bool{}
	for _, name := range forgetListNames() {
		covered[name] = true
	}

	for name := range declared {
		if !covered[name] {
			t.Errorf("%s is labelled by workflow_id but is not in workflowLabelled(); "+
				"its series will never be reclaimed when a workflow is deleted", name)
		}
	}
}

// forgetListNames mirrors workflowLabelled() by name. Kept next to it so the
// two are edited together.
func forgetListNames() []string {
	return []string{
		"BackpressureDropTotal",
		"BackpressureSpillTotal",
		"DeadLetterCount",
		"DeadLetterErrors",
		"IdempotencyConflictsTotal",
		"IdempotencyDedupTotal",
		"IdempotencyKeysTotal",
		"IdempotencyLatency",
		"IdempotencyMissingTotal",
		"MessageErrors",
		"MessagesDroppedNoTarget",
		"MessagesProcessed",
		"PostgresSlotLag",
		"ProcessingLatency",
		"SinkWriteCount",
		"SinkWriteErrors",
		"SubSourceBackoff",
		"TxGroupInDoubt",
		"TxGroupReaped",
		"WorkflowNodeErrors",
		"WorkflowNodeProcessed",
	}
}
