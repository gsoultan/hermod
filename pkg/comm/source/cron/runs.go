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
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// Metadata every run carries, for templates as {{meta.<key>}}.
const (
	// MetaScheduledAt is when this run was due, RFC 3339 in UTC.
	MetaScheduledAt = "scheduled_at"
	// MetaLastSuccessAt is when the last run that succeeded was due, RFC 3339
	// in UTC, or empty before the first. It moves only when a run succeeds,
	// so "changed since then" loses nothing to a failed run:
	//
	//	WHERE "LastUpdated" >= COALESCE(NULLIF({{meta.last_success_at}}, '')::timestamptz, now() - interval '1 day')
	MetaLastSuccessAt = "last_success_at"
	// MetaSkippedTicks is how many ticks were skipped since the last run
	// because it was still going.
	MetaSkippedTicks = "skipped_ticks"
)

// defaultMaxRunDuration is how long a run may go before the schedule gives up
// waiting for it. It is a safety valve for a run the engine never reports
// back on, not a time limit on the run's steps, which have their own.
const defaultMaxRunDuration = time.Hour

// SetMaxRunDuration sets how long a run may go before the next tick stops
// waiting for it. Zero or less keeps the default.
func (s *CronSource) SetMaxRunDuration(d time.Duration) {
	if d <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxRun = d
}

// stateLastSuccess is the source-state key that keeps the last success across
// restarts.
const stateLastSuccess = "last_success_at"

// fire is one tick of the schedule. It queues a run, unless the last one has
// not finished: then the tick is skipped and counted. Runs of a schedule used
// to overlap whenever one took longer than the interval, and overlapping runs
// of the same work slow each other down until every one times out.
func (s *CronSource) fire(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running != "" {
		if now.Sub(s.scheduledAt[s.running]) <= s.maxRun {
			s.skipped++
			return
		}
		// Given up on as failed: its report, if it ever comes, is ignored.
		delete(s.scheduledAt, s.running)
		s.running = ""
	}

	msg := message.AcquireMessage()
	// One id per firing. Traces, runs and idempotency keys are all keyed by
	// message id; "cron_<entry>" was the same for every firing, so every run a
	// schedule made became one trace.
	id := "cron_" + uuid.NewString()
	msg.SetID(id)
	msg.SetOperation(hermod.OpSnapshot)
	msg.SetTable("cron")
	if s.Payload != "" {
		var data map[string]any
		if err := json.Unmarshal([]byte(s.Payload), &data); err == nil {
			msg.SetAfter([]byte(s.Payload))
		}
	}
	msg.SetMetadata(MetaScheduledAt, stamp(now))
	msg.SetMetadata(MetaLastSuccessAt, stamp(s.lastSuccess))
	msg.SetMetadata(MetaSkippedTicks, strconv.Itoa(s.skipped))

	select {
	case s.ch <- msg:
		s.running = id
		if s.scheduledAt == nil {
			s.scheduledAt = make(map[string]time.Time)
		}
		s.scheduledAt[id] = now
		s.skipped = 0
	default:
		message.ReleaseMessage(msg)
	}
}

// RunFinished ends the run msg belongs to, and moves the last success to when
// it was due if it succeeded. It implements hermod.RunFinisher.
func (s *CronSource) RunFinished(msg hermod.Message, ok bool) {
	if msg == nil {
		return
	}
	id := msg.ID()
	s.mu.Lock()
	defer s.mu.Unlock()
	due, known := s.scheduledAt[id]
	if !known {
		return
	}
	delete(s.scheduledAt, id)
	if s.running == id {
		s.running = ""
	}
	if ok && due.After(s.lastSuccess) {
		s.lastSuccess = due
	}
}

// GetState keeps the last success across restarts. It implements
// hermod.Stateful.
func (s *CronSource) GetState() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastSuccess.IsZero() {
		return nil
	}
	return map[string]string{stateLastSuccess: stamp(s.lastSuccess)}
}

// SetState restores what GetState saved. A value that does not parse is
// ignored: the next run then reads its whole default window, which is the
// safe direction.
func (s *CronSource) SetState(state map[string]string) {
	t, err := time.Parse(time.RFC3339Nano, state[stateLastSuccess])
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSuccess = t
}

// stamp formats t as RFC 3339 in UTC, or empty for the zero time.
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
