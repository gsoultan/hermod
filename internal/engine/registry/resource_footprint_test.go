// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
	_ "modernc.org/sqlite"
)

// --- The five-second stats flusher wrote rows that had not moved ------------
//
// runStatusFlusher runs flushStatsToStorage every five seconds and, for every
// engine, issued UpdateWorkflowStats and UpdateWorkflowStatus unconditionally.
// An idle deployment with N workflows therefore made 2N UPDATEs every five
// seconds, for ever, each one re-storing the value already in the row -- on
// SQLite every one of them an fsync, on PostgreSQL every one a dead tuple.

// statsStorage counts what the flusher writes and can refuse a write once.
type statsStorage struct {
	testutil.BaseMockStorage

	mu          sync.Mutex
	statsWrites int
	statusWrite int
	failStats   bool
}

func (s *statsStorage) UpdateWorkflowStats(context.Context, string, uint64, uint64, uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statsWrites++
	if s.failStats {
		s.failStats = false
		return errors.New("storage is busy")
	}
	return nil
}

func (s *statsStorage) UpdateWorkflowStatus(context.Context, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusWrite++
	return nil
}

func (s *statsStorage) counts() (stats, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statsWrites, s.statusWrite
}

func newFlushRegistry(t *testing.T, store *statsStorage) *Registry {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := &Registry{
		engines: map[string]*activeEngine{
			"wf-1": {engine: pkgengine.NewEngine(nil, nil, nil), isWorkflow: true, workflow: storage.Workflow{ID: "wf-1"}},
		},
		storage: store,
		logger:  quietLogger{},
		ctx:     ctx,
		cancel:  cancel,
	}
	return r
}

func TestFlushStatsToStorage_WritesOnlyWhatChanged(t *testing.T) {
	store := &statsStorage{}
	r := newFlushRegistry(t, store)

	for range 3 {
		r.flushStatsToStorage()
	}

	stats, status := store.counts()
	if stats != 1 {
		t.Errorf("three idle flushes wrote the stats row %d times, want 1: every extra "+
			"write is an fsync re-storing the value already there", stats)
	}
	if status != 1 {
		t.Errorf("three idle flushes wrote the status row %d times, want 1", status)
	}
}

func TestFlushStatsToStorage_RetriesARefusedWrite(t *testing.T) {
	store := &statsStorage{failStats: true}
	r := newFlushRegistry(t, store)

	r.flushStatsToStorage() // refused
	r.flushStatsToStorage() // must try again, or the row stays stale for ever

	stats, _ := store.counts()
	if stats != 2 {
		t.Errorf("a refused stats write was retried %d times, want exactly 1 retry (2 writes)", stats-1)
	}
}

// --- Audit retention: once per sweep, at the longest window -----------------
//
// audit_logs is one table for the whole deployment, but purgeRetention ran
// PurgeAuditLogs once per workflow with that workflow's own cutoff. So N
// workflows meant N full-range deletes an hour, and the shortest window among
// them decided what every workflow's audit trail kept.

func TestPurgeRetention_SweepsAuditLogsOnceAtTheLongestWindow(t *testing.T) {
	store := &retentionStorage{workflows: []storage.Workflow{
		{ID: "wf-short", AuditRetention: "7d"},
		{ID: "wf-long", AuditRetention: "365d"},
		{ID: "wf-unset"},
	}}
	reg := newRetentionRegistry(t, store)

	reg.purgeRetention()

	audits := store.purgedAudits()
	if len(audits) != 1 {
		t.Fatalf("audit logs were purged %d times in one sweep, want 1", len(audits))
	}
	want, _ := parseDuration("365d")
	if got := time.Since(audits[0]); got < want-time.Minute || got > want+time.Minute {
		t.Errorf("purged audit logs older than %v, want about %v: the shortest window "+
			"must not decide what the longest one keeps", got, want)
	}
}

// --- A deleted workflow left its in-process state behind ---------------------
//
// piiStats, the supervisor's restart history and the node-state cache are all
// keyed by workflow ID and nothing removed an entry when the workflow was
// deleted. Each is small, but a deployment that creates and deletes workflows
// (every CI run does) grows them for the life of the process.

func TestForgetWorkflow_DropsEveryInProcessRecordOfIt(t *testing.T) {
	store := &retentionStorage{}
	reg := newRetentionRegistry(t, store)
	reg.piiStats = map[string]*PIIStats{
		"gone": {Discoveries: map[string]uint64{"email": 3}},
		"kept": {Discoveries: map[string]uint64{"email": 1}},
	}
	reg.supervisor = newSupervisorState()
	reg.supervisor.allow("gone", time.Now())
	reg.supervisor.allow("kept", time.Now())
	reg.nodeStates = map[string]any{"gone:n1": 1, "gone:n2": 2, "kept:n1": 3}

	reg.ForgetWorkflow(context.Background(), "gone")

	if _, ok := reg.piiStats["gone"]; ok {
		t.Error("piiStats still holds the deleted workflow")
	}
	if _, ok := reg.piiStats["kept"]; !ok {
		t.Error("piiStats lost a workflow that was not deleted")
	}
	if _, ok := reg.supervisor.records["gone"]; ok {
		t.Error("the supervisor still holds the deleted workflow's restart history")
	}
	if _, ok := reg.supervisor.records["kept"]; !ok {
		t.Error("the supervisor lost a workflow that was not deleted")
	}
	for _, key := range []string{"gone:n1", "gone:n2"} {
		if _, ok := reg.nodeStates[key]; ok {
			t.Errorf("nodeStates still holds %s", key)
		}
	}
	if _, ok := reg.nodeStates["kept:n1"]; !ok {
		t.Error("nodeStates lost a workflow that was not deleted")
	}
}

// --- A deleted source left its connection pool open --------------------------
//
// GetOrOpenDB keeps one *sql.DB per source. Deleting the source removed the row
// and nothing else: the pool, its idle connections and database/sql's opener
// goroutine stayed until the process exited.

func TestCloseSourcePools_ClosesThePoolOfADeletedSource(t *testing.T) {
	open := func(t *testing.T) *sql.DB {
		t.Helper()
		db, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	gone, kept := open(t), open(t)
	t.Cleanup(func() { _ = kept.Close() })
	r := &Registry{logger: quietLogger{}, dbPool: map[string]pooledDB{
		"gone|a": {sourceID: "gone", db: gone},
		"gone|b": {sourceID: "gone", db: open(t)},
		"kept|a": {sourceID: "kept", db: kept},
	}}

	r.CloseSourcePools("gone")

	if err := gone.PingContext(t.Context()); err == nil {
		t.Error("the deleted source's pool is still open")
	}
	if err := kept.PingContext(t.Context()); err != nil {
		t.Errorf("another source's pool was closed: %v", err)
	}
	r.dbPoolMu.RLock()
	defer r.dbPoolMu.RUnlock()
	if len(r.dbPool) != 1 {
		t.Errorf("dbPool holds %d entries after the delete, want 1", len(r.dbPool))
	}
	if _, ok := r.dbPool["kept|a"]; !ok {
		t.Error("the surviving source's pool was dropped from the map")
	}
}
