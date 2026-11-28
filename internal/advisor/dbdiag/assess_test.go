// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package dbdiag

import (
	"strings"
	"testing"
)

func TestCommandKeepsOnlyTheLeadingKeywords(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{"CREATE INDEX CONCURRENTLY idx_a ON sales(a)", "CREATE INDEX CONCURRENTLY"},
		{"  select * from customers where email = 'x@y.z'", "SELECT"},
		{"WITH t AS (SELECT 1) SELECT * FROM t", "WITH"},
		{"vacuum analyze orders", "VACUUM ANALYZE"},
		{"UPDATE accounts SET balance = 0", "UPDATE"},
		{"<insufficient privilege>", ""},
		{"", ""},
		{"BEGIN; SELECT count(*) FROM t", "SELECT"},
	} {
		if got := command(tc.query); got != tc.want {
			t.Errorf("command(%q) = %q, want %q", tc.query, got, tc.want)
		}
	}
}

// The query text can carry personal data in its literals; nothing past the
// leading keywords may reach the report.
func TestCommandNeverReturnsLiterals(t *testing.T) {
	if got := command("SELECT 'alice@example.com'"); strings.Contains(got, "alice") {
		t.Fatalf("command leaked a literal: %q", got)
	}
}

func snap(sessions ...Session) snapshot {
	return snapshot{ServerVersion: "16.4", ServerPort: 5432, MaxConns: 300, Reserved: 3, InUse: 41, RoleLimit: -1, DBLimit: -1, Sessions: sessions}
}

func checkByID(t *testing.T, r Report, id string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no %q check in %+v", id, r.Checks)
	return Check{}
}

func TestAssessHealthyDatabase(t *testing.T) {
	r := assess(snap(Session{PID: 1, App: "hermod", State: "active", QuerySeconds: 0.2}), 5432)
	for _, id := range []string{"locks", "long_queries", "idle_in_transaction", "limits"} {
		if c := checkByID(t, r, id); c.Status != StatusOK {
			t.Errorf("%s = %s on a healthy database: %s", id, c.Status, c.Detail)
		}
	}
	if r.Route.ThroughPooler {
		t.Error("a server on the dialled port was reported as behind a pooler")
	}
}

// The incident: Hermod's own reports held every connection for hours, and a
// DBA's CREATE INDEX CONCURRENTLY waited on one of them.
func TestAssessNamesHermodsLongQueriesAndWhoTheyBlock(t *testing.T) {
	r := assess(snap(
		Session{PID: 10, App: "hermod", User: "hermod", State: "active", QuerySeconds: 7020, XactSeconds: 7020, WaitType: "IO", WaitEvent: "DataFileRead", Command: "WITH"},
		Session{PID: 11, App: "hermod-sql-steps", User: "hermod", State: "active", QuerySeconds: 4320, XactSeconds: 4320, WaitType: "IO", WaitEvent: "DataFileRead", Command: "SELECT"},
		Session{PID: 20, App: "psql", User: "dba", State: "active", QuerySeconds: 1320, WaitType: "Lock", WaitEvent: "virtualxid", Command: "CREATE INDEX CONCURRENTLY", BlockedBy: []int{10}},
	), 6432)

	long := checkByID(t, r, "long_queries")
	if long.Status != StatusWarn || !strings.Contains(long.Detail, "2 of them Hermod's") {
		t.Errorf("long queries = %s %q", long.Status, long.Detail)
	}
	locks := checkByID(t, r, "locks")
	if locks.Status != StatusWarn || !strings.Contains(locks.Detail, "dba") || !strings.Contains(locks.Detail, "Hermod") {
		t.Errorf("locks check does not say the dba waits on Hermod: %s %q", locks.Status, locks.Detail)
	}
	waits := checkByID(t, r, "waits")
	if !strings.Contains(waits.Detail, "IO: DataFileRead") {
		t.Errorf("waits = %q", waits.Detail)
	}
	if !r.Route.ThroughPooler {
		t.Error("server on 5432 dialled on 6432 is not reported as behind a pooler")
	}
	if len(r.Hermod) != 2 || r.Hermod[0].PID != 10 {
		t.Errorf("Hermod sessions = %+v, want the two reports longest first", r.Hermod)
	}
	if len(r.Others) != 1 || r.Others[0].User != "dba" || len(r.Others[0].BlockedBy) != 1 {
		t.Errorf("other sessions = %+v", r.Others)
	}
}

func TestAssessIdleInTransaction(t *testing.T) {
	r := assess(snap(Session{PID: 5, App: "report-tool", User: "bi", State: "idle in transaction", StateSeconds: 900, XactSeconds: 900}), 5432)
	if c := checkByID(t, r, "idle_in_transaction"); c.Status != StatusWarn || !strings.Contains(c.Detail, "bi") {
		t.Errorf("idle in transaction = %s %q", c.Status, c.Detail)
	}
}

func TestAssessServerNearItsConnectionLimit(t *testing.T) {
	s := snap()
	s.InUse = 290
	if c := checkByID(t, assess(s, 5432), "limits"); c.Status != StatusErr {
		t.Errorf("290 of 297 connections in use = %s %q", c.Status, c.Detail)
	}
	s = snap()
	s.RoleLimit = 20
	s.Sessions = make([]Session, 0)
	s.OwnUser = 19
	if c := checkByID(t, assess(s, 5432), "limits"); c.Status != StatusErr || !strings.Contains(c.Detail, "role") {
		t.Errorf("role at 19 of 20 = %s %q", c.Status, c.Detail)
	}
}

// Without pg_read_all_stats, other users' rows hide their state and query.
// The report says so instead of calling the database healthy.
func TestAssessSaysWhatItCouldNotSee(t *testing.T) {
	r := assess(snap(Session{PID: 7, User: "other", Hidden: true}), 5432)
	if r.Limited == "" || !strings.Contains(r.Limited, "pg_monitor") {
		t.Errorf("Limited = %q, want it to name the grant that would show more", r.Limited)
	}
}

func TestAssessPooler(t *testing.T) {
	s := snap()
	s.Pooler = &PoolerStats{Readable: true, PoolSize: 12, ClientActive: 12, ClientWaiting: 8, ServerActive: 12, ServerIdle: 0, MaxWaitSeconds: 31}
	c := checkByID(t, assess(s, 6432), "pooler")
	if c.Status != StatusErr || !strings.Contains(c.Detail, "8 clients waiting") {
		t.Errorf("pooler = %s %q", c.Status, c.Detail)
	}
	s.Pooler = &PoolerStats{Reason: "PgBouncer refused the console login (SQLSTATE 08P01)"}
	c = checkByID(t, assess(s, 6432), "pooler")
	if c.Status != StatusUnknown || !strings.Contains(c.Detail, "stats_users") {
		t.Errorf("unreadable pooler = %s %q", c.Status, c.Detail)
	}
}

func TestLocksCountsManyHolders(t *testing.T) {
	r := assess(snap(
		Session{PID: 1, App: "hermod", State: "active", XactSeconds: 7020, Command: "SELECT"},
		Session{PID: 2, App: "hermod", State: "active", XactSeconds: 60, Command: "SELECT"},
		Session{PID: 3, App: "psql", User: "dba", State: "active", WaitType: "Lock", QuerySeconds: 10, Command: "ALTER TABLE", BlockedBy: []int{2, 1}},
	), 5432)
	got := checkByID(t, r, "locks").Detail
	if !strings.Contains(got, "on 2 of Hermod's queries, the longest Hermod, SELECT (in a transaction for 1 h 57 min)") {
		t.Errorf("locks = %q", got)
	}
}
