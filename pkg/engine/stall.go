package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/gsoultan/hermod"
)

// DefaultStallThreshold is how long a pipeline may hold outstanding work without
// completing any of it before it is treated as wedged rather than busy. Slow
// sinks and long retry backoffs are normal, so this is deliberately generous;
// the failure it exists to catch lasts indefinitely, not seconds.
const DefaultStallThreshold = 60 * time.Second

// stallState decides whether a pipeline has stopped making progress.
//
// The distinction it draws is idle vs wedged. A pipeline with nothing to do is
// healthy and must stay quiet; a pipeline holding work it never completes is
// broken and must say so. Hermod previously could not tell the two apart, so a
// wedged workflow reported active=true and "running", logged nothing at any
// level, and quietly accumulated replication lag until someone restarted it by
// hand.
//
// Transitions are edge-triggered: a stall is announced once, and recovery once,
// so a wedge that lasts hours costs two log lines rather than thousands.
type stallState struct {
	lastProcessed uint64
	lastProgress  time.Time
	reported      bool
}

func newStallState(now time.Time) *stallState {
	return &stallState{lastProgress: now}
}

// observe records a sample and reports the transitions crossed by it.
//
// processed is the engine's monotonic completed-message count; workPending is
// whether anything is actually outstanding (buffered messages or source lag).
// It returns stalled on the tick a stall begins and recovered on the tick it
// ends — never both, and never repeatedly for the same episode.
func (s *stallState) observe(processed uint64, workPending bool, now time.Time, threshold time.Duration) (stalled, recovered bool) {
	if processed != s.lastProcessed {
		s.lastProcessed = processed
		s.lastProgress = now
		if s.reported {
			s.reported = false
			return false, true
		}
		return false, false
	}

	// No progress. That is only meaningful if there was something to make
	// progress on: an idle pipeline is not a broken one.
	if !workPending {
		s.lastProgress = now
		return false, false
	}

	if s.reported || now.Sub(s.lastProgress) <= threshold {
		return false, false
	}
	s.reported = true
	return true, false
}

// stalledFor reports how long the pipeline has been without progress.
func (s *stallState) stalledFor(now time.Time) time.Duration {
	return now.Sub(s.lastProgress)
}

// DefaultStreamSilenceInterval is how often a source's push stream is sampled
// for silence when the engine config does not say.
const DefaultStreamSilenceInterval = 10 * time.Second

// streamSilenceWedge reports whether a server-pushed stream has been silent long
// enough to be broken rather than idle.
//
// The threshold comes from the source, because only the source knows the cadence
// its server promises: PostgreSQL sends a keepalive on an idle logical
// replication connection every wal_sender_timeout/2, so silence well past that
// means the stream is no longer being served — even though the socket is open,
// the slot is active, and every health check still passes. A zero threshold
// disables the check, which is correct when the server sends no keepalives at
// all; a zero last-activity means the stream has not started, which is not a
// fault.
func streamSilenceWedge(last time.Time, threshold time.Duration, now time.Time) (wedged bool, silentFor time.Duration) {
	if threshold <= 0 || last.IsZero() {
		return false, 0
	}
	silentFor = now.Sub(last)
	return silentFor > threshold, silentFor
}

// watchForStreamSilence reports a source whose stream has stopped delivering.
//
// This covers the gap progressSample cannot: it only sees work the engine has
// already accepted, so a source that stops handing messages over is
// indistinguishable from one with nothing to send. A replication stream that has
// not even received a keepalive is distinguishable, and it is worth saying so on
// its own terms — "the stream went quiet" points at the source, where the fault
// is, rather than at the sinks.
func (r *Runner) watchForStreamSilence(ctx context.Context) {
	reporter, ok := r.engine.source.(hermod.StreamLivenessReporter)
	if !ok {
		return
	}

	interval := r.engine.config.StreamSilenceInterval
	if interval <= 0 {
		interval = DefaultStreamSilenceInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Edge-triggered, like the progress watchdog: announce an episode once, and
	// keep watching afterwards so a restart the supervisor declines does not
	// leave the stream unmonitored.
	reported := false
	lastProcessed := uint64(0)

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			threshold := reporter.StreamSilenceThreshold()
			wedged, silentFor := streamSilenceWedge(reporter.LastStreamActivity(), threshold, now)

			// Silence alone is not enough. The source's handover to the pipeline
			// blocks while the consumer is behind, and that blocks the receive
			// loop with it — so a slow sink working through a backlog looks
			// exactly like a dead stream from here. What tells them apart is
			// whether anything is still completing: a backlog drains, a wedge
			// does not.
			processed := r.engine.GetStatus().ProcessedCount
			progressing := processed != lastProcessed
			lastProcessed = processed

			if !wedged || progressing {
				if reported {
					reported = false
					r.engine.logger.Info("Source stream is being served again",
						"workflow_id", r.engine.workflowID)
				}
				continue
			}
			if reported {
				continue
			}
			reported = true
			r.reportSilentStream(silentFor, threshold)
		}
	}
}

// reportSilentStream announces a stream that has stopped being served and hands
// it to the supervisor.
func (r *Runner) reportSilentStream(silentFor, threshold time.Duration) {
	reason := fmt.Sprintf("the source stream received nothing for %s, past its %s keepalive deadline",
		silentFor.Round(time.Second), threshold)
	r.engine.logger.Error("Source stream has gone silent: not even a keepalive has arrived",
		"workflow_id", r.engine.workflowID,
		"silent_for", silentFor.Round(time.Second).String(),
		"threshold", threshold.String(),
		"hint", "the replication connection is open but no longer being served; the workflow is being rebuilt")
	r.engine.setStatus("stalled")

	// Hand off on another goroutine: recovery tears this engine down and would
	// otherwise be waiting on this watcher.
	if fn := r.engine.onStall; fn != nil {
		go fn(reason)
	}
}

// watchForStalls turns a silent wedge into a loud one. It samples the engine's
// completed-message count against whether any work is outstanding, and reports
// the transitions — once each — through the log and the workflow status, so a
// stuck pipeline is visible in the UI and to an alerting rule instead of only to
// whoever thinks to compare the sink's row count against the source's.
func (r *Runner) watchForStalls(ctx context.Context) {
	threshold := r.engine.config.StallThreshold
	if threshold <= 0 {
		threshold = DefaultStallThreshold
	}
	// Sample several times per threshold so the reported duration is meaningful.
	interval := max(threshold/4, time.Second)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	state := newStallState(time.Now())

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			processed, pending := r.engine.progressSample()
			stalled, recovered := state.observe(processed, pending, now, threshold)
			switch {
			case stalled:
				reason := fmt.Sprintf("no message completed in %s while work was outstanding", state.stalledFor(now).Round(time.Second))
				r.engine.logger.Error("Pipeline stalled: work is outstanding but nothing has completed",
					"workflow_id", r.engine.workflowID,
					"stalled_for", state.stalledFor(now).String(),
					"processed", processed,
					"hint", "a sink may be unreachable; check sink status and replication lag")
				r.engine.setStatus("stalled")

				// Hand off on another goroutine, because recovery tears this
				// engine down and would otherwise be waiting on this watchdog.
				//
				// Keep watching afterwards rather than returning. The supervisor
				// can decline — its budget may be spent, or a previous restart
				// may still be settling — and a watchdog that exited on handoff
				// left that engine with no supervision at all for the rest of its
				// life. Re-reporting is not a risk: the state machine is
				// edge-triggered, so this episode is announced exactly once
				// however long it lasts.
				if fn := r.engine.onStall; fn != nil {
					go fn(reason)
				}
			case recovered:
				r.engine.logger.Info("Pipeline recovered: messages are completing again",
					"workflow_id", r.engine.workflowID,
					"processed", processed)
				r.engine.setStatus("running")
			}
		}
	}
}

// progressSample reports the completed-message count and whether any work is
// outstanding.
//
// Outstanding means queued in the ingestion buffer, buffered in a sink writer,
// or reported as source lag — the ways a pipeline can be holding work it has not
// finished. Note the limit of this signal: it only sees work the engine has
// already accepted. A source that stops handing messages over at all looks
// identical to a source with nothing to send, so a wedge upstream of the buffer
// is not detectable from here and has to be caught by the source itself.
func (e *Engine) progressSample() (processed uint64, workPending bool) {
	if b, ok := e.buffer.(interface{ Depth() (int, int) }); ok {
		if queued, _ := b.Depth(); queued > 0 {
			workPending = true
		}
	}

	status := e.GetStatus()
	if !workPending {
		for _, fill := range status.SinkBufferFill {
			if fill > 0 {
				workPending = true
				break
			}
		}
	}

	// Ask the source directly rather than inferring from lag.
	//
	// A source that can report what it is owed is authoritative, and lag is not
	// a substitute for it: lag counts WAL the server has produced since this
	// pipeline last confirmed a position, and the server's position moves on
	// every write to that instance — other tables, other databases, autovacuum.
	// An idle workflow on a busy server reports permanently growing lag while
	// owing nothing at all. Treating that as outstanding work restarts healthy
	// pipelines in a loop, which is a worse failure than the one this watchdog
	// exists to fix.
	if !workPending {
		pending, known := false, false
		if pw, ok := e.source.(hermod.PendingWorkReporter); ok {
			pending, known = pw.PendingWork()
		}
		switch {
		case known:
			workPending = pending
		case status.Lag > 0:
			// Fall back to lag for sources that cannot be more precise. It
			// over-reports on a busy server, but under-reporting would hide the
			// wedge entirely.
			workPending = true
		default:
			// status.Lag is published by the periodic health check, so it is
			// stale or absent exactly when things are going wrong. Ask directly.
			if lr, ok := e.source.(hermod.LagReporter); ok {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				lag, err := lr.GetLag(ctx)
				cancel()
				workPending = err == nil && lag > 0
			}
		}
	}
	return status.ProcessedCount, workPending
}

// hasSinks reports whether this workflow has any sink writers at all. Used to
// tell "a filter dropped this message" apart from "this workflow has sinks and
// resolved none of them", which is data loss.
func (e *Engine) hasSinks() bool {
	e.stopMu.Lock()
	defer e.stopMu.Unlock()
	return len(e.sinkWriters) > 0
}

// unroutableWarnInterval throttles the unroutable-message report. A sink outage
// makes every message unroutable, so one line per message would bury the very
// signal it exists to raise.
const unroutableWarnInterval = 30 * time.Second

// reportUnroutable reports messages the workflow resolved no sink for, at most
// once per interval, with a running total so the scale is visible.
//
// The wording follows what actually happens next, because the two outcomes
// could not be more different for the operator reading it. With a dead-letter
// sink the message is parked there and the source is acknowledged: nothing is
// lost, look in the DLQ. Without one the message is deliberately NOT
// acknowledged — it stays on the source and is redelivered on the next run.
// This line used to claim "the source has been acknowledged" unconditionally,
// which for the no-DLQ branch told the operator their data was gone at the
// exact moment the engine was preserving it.
func (e *Engine) reportUnroutable(m hermod.Message) {
	e.unroutableCount.Add(1)

	now := time.Now()
	last := e.unroutableLastLog.Load()
	if last != nil && now.Sub(*last) < unroutableWarnInterval {
		return
	}
	if !e.unroutableLastLog.CompareAndSwap(last, &now) {
		return
	}

	id := ""
	if m != nil {
		id = m.ID()
	}
	hint := "no dead-letter sink is configured; these messages are NOT acknowledged — " +
		"they remain on the source and will be redelivered, which retains WAL/queue " +
		"backlog until a sink target resolves again"
	if e.deadLetterSink != nil {
		hint = "these messages are parked in the dead-letter sink and the source is " +
			"acknowledged; recover them from the DLQ"
	}
	e.logger.Error("Messages delivered nowhere: the workflow has sinks but resolved no target",
		"workflow_id", e.workflowID,
		"dropped_total", e.unroutableCount.Load(),
		"example_message_id", id,
		"hint", hint)
}
