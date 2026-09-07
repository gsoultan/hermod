package engine

import (
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
)

// TestHealthCheckDoesNotClearAStall covers an ordering bug between the two
// things that write the engine status.
//
// The stall watchdog owns stall detection: it sets "stalled" when nothing has
// completed while work is outstanding, and clears it again when it observes
// recovery. checkHealth is a separate periodic pass that pings the sinks, and
// it ended with an unconditional
//
//	if allSinksOk && srcStatus == "running" { setStatus("running") }
//
// A wedged sink is not an unreachable one — it accepts the connection and
// answers Ping, it simply never completes a write. So allSinksOk stays true,
// and the next health pass overwrote "stalled" with "running". The stall was
// still real and the watchdog had already told the supervisor about it, but the
// status the UI reads said the workflow was healthy: exactly the silent failure
// the watchdog exists to make loud.
//
// It surfaced as an intermittent CI failure rather than a reliable one, because
// it depends on a health tick landing between the stall being set and anything
// reading the status.
func TestHealthCheckDoesNotClearAStall(t *testing.T) {
	eng := NewEngine(idleSource{}, []hermod.Sink{&mockSink{}}, buffer.NewRingBuffer(8))
	cfg := stallTestConfig(50 * time.Millisecond)
	eng.SetConfig(cfg)

	r := &Runner{engine: eng, ctx: t.Context()}

	// The watchdog has detected a stall.
	eng.setStatus("stalled")

	// A health pass runs while the pipeline is still wedged. Every sink pings
	// fine, because a sink that never finishes a write is still reachable.
	r.checkHealth(time.Millisecond)

	if got := eng.GetStatus().EngineStatus; got != "stalled" {
		t.Errorf("engine status = %q after a health check, want %q: the health pass "+
			"overwrote a stall the watchdog had already reported, so the UI shows a "+
			"wedged workflow as healthy", got, "stalled")
	}
}

// The sequential case above was fixed with an `if engStatus != "stalled"`
// around the write, testing a value read earlier in checkHealth. That closes
// the tick-after-the-stall ordering but not the one inside a single tick: the
// read and the write are separate lock acquisitions, so a watchdog that sets
// "stalled" between them is overwritten by a guard that already decided the
// pipeline was fine. It is a narrow window, which is why the CI failure it
// caused survived the first fix and kept appearing at the same assertion.
//
// Enforcing the exclusion in the write itself is what actually closes it.
func TestHealthCheckLosingTheRaceStillDoesNotClearAStall(t *testing.T) {
	for i := range 300 {
		eng := NewEngine(idleSource{}, []hermod.Sink{&mockSink{}}, buffer.NewRingBuffer(8))
		eng.SetConfig(stallTestConfig(50 * time.Millisecond))
		r := &Runner{engine: eng, ctx: t.Context()}
		eng.setStatus("running")

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); eng.setStatus("stalled") }()
		go func() { defer wg.Done(); r.checkHealth(time.Millisecond) }()
		wg.Wait()

		// Either order is legal. The health pass may publish "running" first
		// and have the watchdog overwrite it; what must not happen is the
		// health pass landing last and clearing a stall already reported to the
		// supervisor.
		if got := eng.GetStatus().EngineStatus; got != "stalled" {
			t.Fatalf("iteration %d: engine status = %q, want %q: a health pass cleared "+
				"a stall the watchdog had already reported", i, got, "stalled")
		}
	}
}
