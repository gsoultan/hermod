// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package registry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

const (
	// maxDrainFailuresInARow ends a drain once this many replays in a row have
	// failed: the cause is most likely still there, and every other waiting
	// dead letter would fail the same way.
	maxDrainFailuresInARow = 5
	// maxDrainAttempts bounds one drain, whatever keeps arriving meanwhile.
	maxDrainAttempts = 10_000
	// drainPage is how many waiting dead letters a drain lists at a time.
	drainPage = 100
	// drainReplayTimeout bounds one replay within a drain.
	drainReplayTimeout = 2 * time.Minute
)

// Replay outcomes.
const (
	ReplayReplayed    = "replayed"
	ReplayFailed      = "failed"
	ReplayBusy        = "busy"
	ReplayNotReplayed = "not_replayed"
)

// DeadLetterReplayOutcome is how one replay went.
type DeadLetterReplayOutcome struct {
	// Status is ReplayReplayed, ReplayFailed (it ran and failed again),
	// ReplayBusy (it is already being replayed) or ReplayNotReplayed (it
	// could not be run at all; Error says why).
	Status string
	Error  string
	Result *RunResult
}

// ReplayAndSettleDeadLetter replays d through wf and marks it replayed when it
// was delivered. A replay that fails again leaves it waiting, with the attempt
// in its history. One dead letter is replayed by one caller at a time in this
// process, so a click and a drain cannot deliver it twice.
func (r *Registry) ReplayAndSettleDeadLetter(ctx context.Context, ds storage.DeadLetterStore, wf storage.Workflow,
	d storage.DeadLetter, opt DeadLetterReplay, by, detail string) DeadLetterReplayOutcome {
	if _, busy := r.replayingDeadLetters.LoadOrStore(d.ID, struct{}{}); busy {
		return DeadLetterReplayOutcome{Status: ReplayBusy, Error: "this dead letter is already being replayed"}
	}
	defer r.replayingDeadLetters.Delete(d.ID)

	res, err := r.ReplayDeadLetter(ctx, wf, d, opt)
	if err != nil {
		return DeadLetterReplayOutcome{Status: ReplayNotReplayed, Error: err.Error()}
	}
	out := DeadLetterReplayOutcome{Result: &res}
	if res.Status == RunFailed {
		out.Status, out.Error = ReplayFailed, firstStepError(res)
		_ = ds.AddDeadLetterEvent(ctx, d.ID, storage.NewDeadLetterEvent("replay_failed", by, detail+": "+out.Error))
		return out
	}
	if err := ds.SettleDeadLetter(ctx, d.ID, storage.DeadLetterReplayed, storage.NewDeadLetterEvent("replayed", by, detail)); err != nil &&
		!errors.Is(err, storage.ErrDeadLetterSettled) {
		r.logger.Error("A dead letter was replayed but could not be marked replayed", "dead_letter_id", d.ID, "error", err)
	}
	out.Status = ReplayReplayed
	return out
}

func firstStepError(res RunResult) string {
	for _, s := range res.Steps {
		if s.Error != "" {
			return s.Error
		}
	}
	return "the message failed again"
}

// ReplayWaitingDeadLetters replays, one at a time, the dead letters wf has
// waiting on the Dead Letters page, each from the node or sink that failed it,
// and settles each one that is delivered. It stops after
// maxDrainFailuresInARow failures in a row. It reports how many went through
// and how many failed again.
func (r *Registry) ReplayWaitingDeadLetters(ctx context.Context, wf storage.Workflow, by string) (replayed, failed int) {
	ds, ok := r.store().(storage.DeadLetterStore)
	if !ok {
		return 0, 0
	}
	d := drain{r: r, ds: ds, wf: wf, by: by}
	for ctx.Err() == nil {
		page, _, err := ds.ListDeadLetters(ctx, storage.DeadLetterFilter{
			WorkflowID: wf.ID, Status: storage.DeadLetterQueued, Limit: drainPage, Offset: d.left,
		})
		if err != nil {
			r.logger.Error("Could not list the dead letters to drain", "workflow_id", wf.ID, "error", err)
			break
		}
		if len(page) == 0 || !d.page(ctx, page) {
			break
		}
	}
	return d.replayed, d.failed
}

// drain is one run of ReplayWaitingDeadLetters.
type drain struct {
	r  *Registry
	ds storage.DeadLetterStore
	wf storage.Workflow
	by string
	// left counts the waiting dead letters this drain left behind. They stay
	// in the list, so the next page starts after them.
	left, inARow, attempts, replayed, failed int
}

// page replays one page of waiting dead letters. It reports whether the
// drain should go on to the next page.
func (d *drain) page(ctx context.Context, page []storage.DeadLetter) bool {
	for _, item := range page {
		if ctx.Err() != nil || d.attempts >= maxDrainAttempts {
			return false
		}
		if !d.one(ctx, item.ID) {
			return false
		}
	}
	return true
}

// one replays the dead letter id. It reports whether the drain should go on.
func (d *drain) one(ctx context.Context, id string) bool {
	// The list leaves the payload out; the replay needs it.
	dl, err := d.ds.GetDeadLetter(ctx, id)
	if err != nil {
		d.left++
		return true
	}
	if dl.Status != storage.DeadLetterQueued {
		return true // settled meanwhile, so it has left the list
	}
	d.attempts++
	one, cancel := context.WithTimeout(ctx, drainReplayTimeout)
	o := d.r.ReplayAndSettleDeadLetter(one, d.ds, d.wf, dl, DeadLetterReplay{}, d.by, "drained")
	cancel()
	switch o.Status {
	case ReplayReplayed:
		d.replayed++
		d.inARow = 0
		return true
	case ReplayBusy:
		d.left++
		return true
	case ReplayNotReplayed:
		_ = d.ds.AddDeadLetterEvent(ctx, dl.ID, storage.NewDeadLetterEvent("replay_failed", d.by, "drained: "+o.Error))
	}
	d.left++
	d.failed++
	d.inARow++
	if d.inARow >= maxDrainFailuresInARow {
		d.r.logger.Warn("Stopped draining dead letters: the last replays all failed",
			"workflow_id", d.wf.ID, "failures_in_a_row", d.inARow, "last_error", o.Error)
		return false
	}
	return true
}

// DrainWorkflowDLQ replays, in the background, the dead letters the workflow
// has waiting on the Dead Letters page. It returns once the drain has started.
func (r *Registry) DrainWorkflowDLQ(ctx context.Context, id string) error {
	st := r.store()
	if st == nil {
		return errors.New("registry storage is not initialized")
	}
	if _, ok := st.(storage.DeadLetterStore); !ok {
		return storage.ErrDeadLettersUnsupported
	}
	wf, err := st.GetWorkflow(ctx, id)
	if err != nil {
		return fmt.Errorf("workflow %s: %w", id, err)
	}
	r.drainDeadLetters(wf, "Drain DLQ")
	return nil
}

// drainDeadLetters starts a background drain of wf's waiting dead letters,
// unless one is already running for it. It ends with the registry.
func (r *Registry) drainDeadLetters(wf storage.Workflow, by string) {
	if _, ok := r.store().(storage.DeadLetterStore); !ok {
		return
	}
	if _, running := r.drainingDeadLetters.LoadOrStore(wf.ID, struct{}{}); running {
		return
	}
	go func() {
		defer r.drainingDeadLetters.Delete(wf.ID)
		replayed, failed := r.ReplayWaitingDeadLetters(r.ctx, wf, by)
		if replayed+failed == 0 {
			return
		}
		r.logger.Info("Drained dead letters", "workflow_id", wf.ID, "by", by, "replayed", replayed, "failed", failed)
		r.broadcastLog(wf.ID, "INFO", fmt.Sprintf("%s replayed %d dead letter(s); %d failed again and are still waiting",
			by, replayed, failed))
	}()
}
