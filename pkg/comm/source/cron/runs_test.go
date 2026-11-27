// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package cron

import (
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// pending is every message the schedule has queued and Read has not taken.
func pending(s *CronSource) []hermod.Message {
	var out []hermod.Message
	for {
		select {
		case m := <-s.ch:
			out = append(out, m)
		default:
			return out
		}
	}
}

// A schedule used to fire whether or not the last run had finished. A run that
// took longer than the interval -- a sync that reads a day of changes every
// minute -- overlapped the next one, the two fought over the same rows and
// connections, each ran slower than the one before, and every run failed on a
// timeout. A tick that finds a run still going is now skipped; the next run's
// "since the last success" covers what it would have done.
func TestATickIsSkippedWhileTheLastRunIsStillGoing(t *testing.T) {
	s := NewCronSource("@every 1m", "")
	t0 := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)

	s.fire(t0)
	s.fire(t0.Add(time.Minute))
	s.fire(t0.Add(2 * time.Minute))
	got := pending(s)
	if len(got) != 1 {
		t.Fatalf("three ticks during one run queued %d runs, want 1", len(got))
	}

	s.RunFinished(got[0], true)
	s.fire(t0.Add(3 * time.Minute))
	next := pending(s)
	if len(next) != 1 {
		t.Fatalf("a tick after the run finished queued %d runs, want 1", len(next))
	}
	if v, _ := hermod.MetadataValue(next[0], MetaSkippedTicks); v != "2" {
		t.Errorf("%s = %q, want 2", MetaSkippedTicks, v)
	}
}

// A run that failed still ends the run: the schedule goes on.
func TestAFailedRunDoesNotStopTheSchedule(t *testing.T) {
	s := NewCronSource("@every 1m", "")
	t0 := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	s.fire(t0)
	s.RunFinished(pending(s)[0], false)
	s.fire(t0.Add(time.Minute))
	if n := len(pending(s)); n != 1 {
		t.Fatalf("after a failed run the next tick queued %d runs, want 1", n)
	}
}

// Each run says when the last successful run was scheduled, so a query can
// read what changed since then instead of a fixed window such as "the last
// day". A fixed window re-reads the whole window every time, and loses every
// change older than the window if runs fail for longer than it. The point
// moves only on success, so a failed run loses nothing: the next one reads
// from the same point.
func TestEachRunCarriesTheLastSuccessfulRun(t *testing.T) {
	s := NewCronSource("@every 1m", "")
	t0 := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	stamp := func(tm time.Time) string { return tm.UTC().Format(time.RFC3339Nano) }

	s.fire(t0)
	first := pending(s)[0]
	if v, _ := hermod.MetadataValue(first, MetaLastSuccessAt); v != "" {
		t.Errorf("the first run says the last success was %q, want empty", v)
	}
	if v, _ := hermod.MetadataValue(first, MetaScheduledAt); v != stamp(t0) {
		t.Errorf("%s = %q, want %q", MetaScheduledAt, v, stamp(t0))
	}
	s.RunFinished(first, true)

	s.fire(t0.Add(time.Minute))
	second := pending(s)[0]
	if v, _ := hermod.MetadataValue(second, MetaLastSuccessAt); v != stamp(t0) {
		t.Errorf("after a success at %s the next run says %q", stamp(t0), v)
	}
	s.RunFinished(second, false)

	s.fire(t0.Add(2 * time.Minute))
	third := pending(s)[0]
	if v, _ := hermod.MetadataValue(third, MetaLastSuccessAt); v != stamp(t0) {
		t.Errorf("a failed run moved the last success to %q, want it kept at %q", v, stamp(t0))
	}
}

// The point survives a restart through the source's saved state.
func TestTheLastSuccessIsKeptInTheSourceState(t *testing.T) {
	s := NewCronSource("@every 1m", "")
	t0 := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	s.fire(t0)
	s.RunFinished(pending(s)[0], true)

	restarted := NewCronSource("@every 1m", "")
	restarted.SetState(s.GetState())
	restarted.fire(t0.Add(time.Hour))
	if v, _ := hermod.MetadataValue(pending(restarted)[0], MetaLastSuccessAt); v != t0.Format(time.RFC3339Nano) {
		t.Errorf("after a restart the last success is %q, want %q", v, t0.Format(time.RFC3339Nano))
	}
}

var _ hermod.RunFinisher = (*CronSource)(nil)
var _ hermod.Stateful = (*CronSource)(nil)

// A run the engine never reports back on -- lost at a shutdown before it was
// buffered, say -- must not stop the schedule for good. Past the longest a run
// may take it is given up on as failed, and the tick goes ahead.
func TestARunThatNeverEndsIsGivenUpOn(t *testing.T) {
	s := NewCronSource("@every 1m", "")
	s.SetMaxRunDuration(time.Hour)
	t0 := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	s.fire(t0)
	lost := pending(s)[0]

	s.fire(t0.Add(59 * time.Minute))
	if n := len(pending(s)); n != 0 {
		t.Fatalf("a tick within the longest run time queued %d runs, want 0", n)
	}
	s.fire(t0.Add(61 * time.Minute))
	next := pending(s)
	if len(next) != 1 {
		t.Fatalf("a tick past the longest run time queued %d runs, want 1", len(next))
	}
	if v, _ := hermod.MetadataValue(next[0], MetaLastSuccessAt); v != "" {
		t.Errorf("a run given up on counted as a success: last success %q", v)
	}
	// Its report, if it ever comes, changes nothing.
	s.RunFinished(lost, true)
	if v := s.GetState()[stateLastSuccess]; v != "" {
		t.Errorf("a late report from an abandoned run moved the last success to %q", v)
	}
}
