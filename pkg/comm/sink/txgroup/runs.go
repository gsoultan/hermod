// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package txgroup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gsoultan/hermod/pkg/engine/twopc"
)

// MaxRecentRuns is how many runs a group keeps for its view, newest first.
// Every run is counted; only the latest are kept whole.
const MaxRecentRuns = 50

// RunOutcome is how a run of the group ended.
type RunOutcome string

const (
	// OutcomeCommitted: every member with rows committed them.
	OutcomeCommitted RunOutcome = "committed"
	// OutcomeRolledBack: a member could not write or prepare, and every member
	// rolled back.
	OutcomeRolledBack RunOutcome = "rolled_back"
	// OutcomeInDoubt: the decision to commit is durable but did not reach every
	// member; recovery finishes it.
	OutcomeInDoubt RunOutcome = "in_doubt"
	// OutcomeRefused: the transaction requires a row for every member and the
	// event did not produce one for each, so nothing was written.
	OutcomeRefused RunOutcome = "refused"
)

// RunMember is one member's part in a run.
type RunMember struct {
	ID   string `json:"id"`
	Rows int    `json:"rows"`
	// Failed marks the member whose write or vote ended the run.
	Failed bool `json:"failed,omitempty"`
	// Missing marks a member the event produced no row for, in a refused run.
	Missing bool `json:"missing,omitempty"`
}

// Run is one batch the group wrote, or one event it refused.
type Run struct {
	At         time.Time   `json:"at"`
	Outcome    RunOutcome  `json:"outcome"`
	DurationMS int64       `json:"duration_ms"`
	Members    []RunMember `json:"members"`
	Error      string      `json:"error,omitempty"`
}

// MemberStats is one member's totals across runs.
type MemberStats struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	Committed  uint64 `json:"committed"`
	RolledBack uint64 `json:"rolled_back"`
	InDoubt    uint64 `json:"in_doubt"`
	// Missing counts the events refused for having no row for this member.
	Missing     uint64    `json:"missing"`
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitzero"`
}

// Stats is what a group has recorded about its runs.
type Stats struct {
	Committed  uint64        `json:"committed"`
	RolledBack uint64        `json:"rolled_back"`
	InDoubt    uint64        `json:"in_doubt"`
	Refused    uint64        `json:"refused"`
	Members    []MemberStats `json:"members"`
	// Recent is the latest runs, newest first, at most MaxRecentRuns.
	Recent []Run `json:"recent"`
}

// RunStore is where a group keeps its runs: hermod.StateStore satisfies it.
type RunStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte) error
}

// runLog records the runs of one group. It has its own lock: runs are recorded
// from WriteBatch, under the group's lock, and from ValidateBundle, which the
// engine calls outside it.
type runLog struct {
	mu    sync.Mutex
	stats Stats
	store RunStore
	key   string
}

func newRunLog(members []Member) *runLog {
	l := &runLog{}
	for _, m := range members {
		l.stats.Members = append(l.stats.Members, MemberStats{ID: m.ID, Name: m.Name})
	}
	return l
}

// KeepRunsIn keeps the group's runs in store under key, carrying on from what
// is already there. Call it before the first write. A run that cannot be
// stored is logged, never failed: the record of a write must not undo it.
func (s *Sink) KeepRunsIn(ctx context.Context, store RunStore, key string) {
	prev, err := ReadRuns(ctx, store, key)
	if err != nil && s.logger != nil {
		s.logger.Warn("txgroup: could not read the runs recorded so far; counting from zero",
			"key", key, "error", err)
	}

	l := s.runs
	l.mu.Lock()
	defer l.mu.Unlock()
	l.store, l.key = store, key
	if err != nil {
		return
	}
	l.stats.Committed, l.stats.RolledBack = prev.Committed, prev.RolledBack
	l.stats.InDoubt, l.stats.Refused = prev.InDoubt, prev.Refused
	l.stats.Recent = prev.Recent
	for i := range l.stats.Members {
		for _, p := range prev.Members {
			if p.ID != l.stats.Members[i].ID {
				continue
			}
			name := l.stats.Members[i].Name
			l.stats.Members[i] = p
			if name != "" {
				l.stats.Members[i].Name = name
			}
		}
	}
}

// ReadRuns reads the runs a group keeps under key. A transaction that has
// never run has none, which is not an error.
func ReadRuns(ctx context.Context, store RunStore, key string) (Stats, error) {
	raw, err := store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, twopc.ErrNotFound) {
			return Stats{}, nil
		}
		return Stats{}, fmt.Errorf("txgroup: cannot read runs at %q: %w", key, err)
	}
	if len(raw) == 0 {
		return Stats{}, nil
	}
	var st Stats
	if err := json.Unmarshal(raw, &st); err != nil {
		return Stats{}, fmt.Errorf("txgroup: runs at %q are corrupt: %w", key, err)
	}
	return st, nil
}

// Stats returns a copy of what the group has recorded.
func (s *Sink) Stats() Stats {
	l := s.runs
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.stats
	out.Members = append([]MemberStats(nil), l.stats.Members...)
	out.Recent = append([]Run(nil), l.stats.Recent...)
	return out
}

// record adds a run and, when the group keeps its runs, stores them.
func (s *Sink) record(ctx context.Context, run Run) {
	l := s.runs
	l.mu.Lock()
	l.stats.count(run, s.memberIdx)
	store, key := l.store, l.key
	var data []byte
	var err error
	if store != nil {
		data, err = json.Marshal(l.stats)
	}
	l.mu.Unlock()

	if store == nil {
		return
	}
	if err == nil {
		// Detached: a write that just committed is recorded even when the
		// caller's context ended with it.
		err = store.Set(context.WithoutCancel(ctx), key, data)
	}
	if err != nil && s.logger != nil {
		s.logger.Warn("txgroup: could not store the record of a run; the run itself is unaffected",
			"key", key, "error", err)
	}
}

// count adds a run to the totals, the members' and the recent runs.
func (st *Stats) count(run Run, memberIdx map[string]int) {
	switch run.Outcome {
	case OutcomeCommitted:
		st.Committed++
	case OutcomeRolledBack:
		st.RolledBack++
	case OutcomeInDoubt:
		st.InDoubt++
	case OutcomeRefused:
		st.Refused++
	}
	for _, rm := range run.Members {
		m := &st.Members[memberIdx[rm.ID]]
		switch run.Outcome {
		case OutcomeRefused:
			if rm.Missing {
				m.Missing++
			}
		case OutcomeCommitted:
			m.Committed++
		case OutcomeRolledBack:
			m.RolledBack++
		case OutcomeInDoubt:
			m.InDoubt++
		}
		if rm.Failed {
			m.LastError, m.LastErrorAt = run.Error, run.At
		}
	}
	st.Recent = append([]Run{run}, st.Recent...)
	if len(st.Recent) > MaxRecentRuns {
		st.Recent = st.Recent[:MaxRecentRuns]
	}
}

// outcomeOf says how a write ended, and which member ended it.
func outcomeOf(err error) (RunOutcome, string) {
	if err == nil {
		return OutcomeCommitted, ""
	}
	if errors.Is(err, twopc.ErrCommitIncomplete) {
		return OutcomeInDoubt, ""
	}
	var me *memberError
	if errors.As(err, &me) {
		return OutcomeRolledBack, me.id
	}
	var pe *twopc.ParticipantError
	if errors.As(err, &pe) {
		return OutcomeRolledBack, pe.ID
	}
	return OutcomeRolledBack, ""
}

// memberError is a member's write failing. It keeps the member's ID so the run
// can say which database refused.
type memberError struct {
	id  string
	err error
}

func (e *memberError) Error() string { return fmt.Sprintf("member %q: %v", e.id, e.err) }
func (e *memberError) Unwrap() error { return e.err }
