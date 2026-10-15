package storage

import (
	"encoding/json"
	"testing"
)

// A workflow exported or versioned by an earlier release names a dead-letter
// sink. It reads as a workflow that keeps its dead letters.
func TestAWorkflowNamingADeadLetterSinkReadsAsKeepingDeadLetters(t *testing.T) {
	var wf Workflow
	if err := json.Unmarshal([]byte(`{"id":"wf","name":"n","dead_letter_sink_id":"snk-rabbit","max_retries":3}`), &wf); err != nil {
		t.Fatal(err)
	}
	if !wf.DeadLetters || wf.ID != "wf" || wf.MaxRetries != 3 {
		t.Fatalf("got %+v", wf)
	}

	var off Workflow
	if err := json.Unmarshal([]byte(`{"id":"wf","dead_letter_sink_id":""}`), &off); err != nil {
		t.Fatal(err)
	}
	if off.DeadLetters {
		t.Fatal("an empty dead-letter sink turned dead letters on")
	}

	out, err := json.Marshal(Workflow{ID: "wf", DeadLetters: true})
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	_ = json.Unmarshal(out, &back)
	if back["dead_letters"] != true {
		t.Fatalf("marshalled %s", out)
	}
	if _, ok := back["dead_letter_sink_id"]; ok {
		t.Fatalf("marshalled the removed field: %s", out)
	}
}
