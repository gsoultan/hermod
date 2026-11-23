// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package engine

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/hermod/pkg/comm/reply"
)

// outcomeReportWindow is the most often a workflow writes its summary line.
const outcomeReportWindow = time.Minute

// outcomeReport writes the workflow log's account of what became of the
// messages it processed: one INFO line, at most once per window.
//
// The per-message success line is DEBUG, deliberately — at streaming rates it
// is an unbounded log stream (see TestASuccessfulWriteDoesNotLogPerMessageAtInfo).
// Without anything in its place, a workflow log carried no INFO line at all
// saying a message was ever delivered, so a healthy run read exactly like a run
// that did nothing. Reported on a cron workflow that delivers one message per
// schedule, where that is the only thing anyone wants to know.
//
// The first message after a quiet window is reported at once, so a scheduled
// run shows up as it happens. Anything after it inside the window is counted
// and reported when the window ends (flushDue, on the status ticker) or when
// the engine stops (flush). One line per workflow per minute is the ceiling,
// whatever the rate.
type outcomeReport struct {
	mu     sync.Mutex
	window time.Duration
	last   time.Time
	counts map[reply.Status]int
}

func newOutcomeReport() *outcomeReport {
	return &outcomeReport{window: outcomeReportWindow, counts: make(map[reply.Status]int, 5)}
}

// note counts one message's outcome and reports at once when the window since
// the last line has passed.
func (o *outcomeReport) note(e *Engine, status reply.Status) {
	if o == nil || status == "" {
		return
	}
	o.mu.Lock()
	o.counts[status]++
	due := time.Since(o.last) >= o.window
	var counts map[reply.Status]int
	if due {
		counts = o.takeLocked()
	}
	o.mu.Unlock()
	if due {
		e.logOutcomes(counts)
	}
}

// flushDue reports what was counted once the window has passed.
func (o *outcomeReport) flushDue(e *Engine) {
	o.flushIf(e, func() bool { return time.Since(o.last) >= o.window })
}

// flush reports whatever was counted, now.
func (o *outcomeReport) flush(e *Engine) {
	o.flushIf(e, func() bool { return true })
}

func (o *outcomeReport) flushIf(e *Engine, due func() bool) {
	if o == nil {
		return
	}
	o.mu.Lock()
	if len(o.counts) == 0 || !due() {
		o.mu.Unlock()
		return
	}
	counts := o.takeLocked()
	o.mu.Unlock()
	e.logOutcomes(counts)
}

func (o *outcomeReport) takeLocked() map[reply.Status]int {
	counts := o.counts
	o.counts = make(map[reply.Status]int, 5)
	o.last = time.Now()
	return counts
}

// outcomeWords is the order and the wording of the summary line.
var outcomeWords = []struct {
	status reply.Status
	key    string
	words  string
}{
	{reply.Delivered, "delivered", "delivered"},
	{reply.Completed, "completed", "completed with no sink to write"},
	{reply.Filtered, "filtered", "filtered out"},
	{reply.DeadLettered, "dead_lettered", "dead-lettered"},
	{reply.Failed, "failed", "failed"},
}

func (e *Engine) logOutcomes(counts map[reply.Status]int) {
	total := 0
	parts := make([]string, 0, len(outcomeWords))
	kv := []any{"workflow_id", e.workflowID, "action", "processed"}
	for _, w := range outcomeWords {
		n := counts[w.status]
		if n == 0 {
			continue
		}
		total += n
		parts = append(parts, fmt.Sprintf("%d %s", n, w.words))
		kv = append(kv, w.key, n)
	}
	if total == 0 {
		return
	}
	noun := "messages"
	if total == 1 {
		noun = "message"
	}
	e.logger.Info(fmt.Sprintf("Processed %d %s: %s", total, noun, strings.Join(parts, ", ")), kv...)
}
