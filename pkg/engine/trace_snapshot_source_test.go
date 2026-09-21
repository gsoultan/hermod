package engine

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// A trace step records the message it was handed. Nothing else.
//
// recordTraceStep used to prefer a payload cached in the context under
// hermod.LastTraceSnapshotKey over the message's own ToMap(), as an
// optimisation. It never fired: both producers of that key are in the registry
// (ContextWithPipelineSnapshot, and TestTransformationPipeline's own context),
// and neither context reaches the engine — runPipeline passes its pctx only to
// ApplyTransformation, which does not record engine steps. Measured at zero
// hits across the repo's short suite before the branch was removed.
//
// Dead was the good case. The bad case is the day someone threads that context
// through, because the branch could not tell whether the cached payload
// belonged to this message or this moment — it would file another step's data
// under this one, silently, which is precisely the failure the trace tests in
// this package and in internal/engine/registry exist to catch. The registry's
// own two uses of the key are a different thing and stay: there the pointer is
// created per pipeline run and read one step later, and
// TestTraceStepsRecordTheirOwnMessagesData asserts it carries the right
// message's payload.
//
// So: hand the engine a context carrying someone else's payload and check it is
// ignored.
func TestATraceStepRecordsItsOwnMessageNotACachedContextSnapshot(t *testing.T) {
	recorder := &mockTraceRecorder{}
	e := NewEngine(nil, nil, nil)
	e.SetLogger(&testLogger{})
	e.traceRecorder = recorder
	e.workflowID = "wf-snapshot-source"
	e.config.TraceSampleRate = 1.0

	msg := message.AcquireMessage()
	defer msg.Release()
	msg.SetID("m-own")
	msg.SetData("owner", "m-own")

	// A payload belonging to an entirely different message, of the exact shape
	// the removed branch used to accept.
	foreign := map[string]any{"owner": "someone-else"}
	ctx := context.WithValue(t.Context(), hermod.LastTraceSnapshotKey, &foreign)

	e.RecordTraceStep(ctx, msg, "node-1", time.Now(), nil, nil)

	steps := waitForTraceSteps(t, recorder, "m-own", 1)
	if got := steps[0].After["owner"]; got != "m-own" {
		t.Errorf("the step recorded owner=%v, want %q — it took the payload cached in the context "+
			"instead of the message it was handed", got, "m-own")
	}
}

// waitForTraceSteps polls until n steps have been recorded for a message.
// Recording is detached onto its own goroutine, so the call that triggers it
// returns before the step lands.
func waitForTraceSteps(t *testing.T, rec *mockTraceRecorder, msgID string, n int) []hermod.TraceStep {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if steps := rec.GetSteps(msgID); len(steps) >= n {
			return steps
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d trace step(s) for %s; got %d", n, msgID, len(rec.GetSteps(msgID)))
	return nil
}
