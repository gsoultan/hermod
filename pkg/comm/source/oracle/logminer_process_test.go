// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package oracle

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/gsoultan/hermod"
)

// These tests drive the LogMiner record processor with the records
// V$LOGMNR_CONTENTS returns, in the order it returns them, and assert what
// reaches the pipeline. They need no database: the processor is the part that
// decides what a change *is*, when it is safe to emit and where a restart
// resumes, so it is the part worth pinning exhaustively.

func v(s string) lmValue { return lmValue{Present: true, Raw: s} }

var lmNull = lmValue{Present: true, Null: true}

func ins(scn uint64, xid, rs string, ssn int64, row string, after map[string]lmValue) lmRecord {
	return lmRecord{SCN: scn, Op: lmOpInsert, XID: xid, RSID: rs, SSN: ssn, RowID: row, Owner: "APP", Table: "T", Redo: after}
}

func upd(scn uint64, xid, rs string, ssn int64, row string, redo, undo map[string]lmValue) lmRecord {
	return lmRecord{SCN: scn, Op: lmOpUpdate, XID: xid, RSID: rs, SSN: ssn, RowID: row, Owner: "APP", Table: "T", Redo: redo, Undo: undo}
}

func del(scn uint64, xid, rs string, ssn int64, row string, undo map[string]lmValue) lmRecord {
	return lmRecord{SCN: scn, Op: lmOpDelete, XID: xid, RSID: rs, SSN: ssn, RowID: row, Owner: "APP", Table: "T", Undo: undo}
}

func commit(scn uint64, xid, rs string) lmRecord {
	return lmRecord{SCN: scn, Op: lmOpCommit, XID: xid, RSID: rs}
}

func rollback(scn uint64, xid, rs string) lmRecord {
	return lmRecord{SCN: scn, Op: lmOpRollback, XID: xid, RSID: rs}
}

var lmTypes = map[string]map[string]string{
	"APP.T": {"ID": "NUMBER", "NAME": "VARCHAR2", "AMOUNT": "NUMBER", "CREATED": "DATE"},
}

func feed(t *testing.T, p *lmProcessor, recs ...lmRecord) []lmCommitted {
	t.Helper()
	var out []lmCommitted
	for _, r := range recs {
		if c, ok := p.add(r); ok {
			out = append(out, c)
		}
	}
	return out
}

func TestLogMinerProcessorEmitsInsertUpdateDeleteOnCommit(t *testing.T) {
	p := newLMProcessor(lmPosition{}, lmTypes)

	got := feed(t, p,
		ins(100, "X1", "r1", 1, "AAA", map[string]lmValue{"ID": v("1"), "NAME": v("a"), "AMOUNT": v("1.5"), "CREATED": v("2026-10-11T07:30:18")}),
	)
	if len(got) != 0 {
		t.Fatalf("a change must not be emitted before its transaction commits; got %d", len(got))
	}

	got = feed(t, p,
		commit(101, "X1", "r2"),
		upd(102, "X2", "r3", 0, "AAA",
			map[string]lmValue{"NAME": v("b")},
			map[string]lmValue{"ID": v("1"), "NAME": v("a"), "AMOUNT": v("1.5"), "CREATED": v("2026-10-11T07:30:18")}),
		del(103, "X2", "r4", 0, "AAA",
			map[string]lmValue{"ID": v("1"), "NAME": v("b"), "AMOUNT": v("1.5"), "CREATED": v("2026-10-11T07:30:18")}),
		commit(104, "X2", "r5"),
	)
	if len(got) != 2 {
		t.Fatalf("want 2 committed transactions, got %d", len(got))
	}

	first := got[0]
	if first.XID != "X1" || first.CommitSCN != 101 || len(first.Changes) != 1 {
		t.Fatalf("first transaction = %+v", first)
	}
	c := first.Changes[0]
	if c.Op != hermod.OpCreate || c.Before != nil {
		t.Fatalf("insert: op=%v before=%v", c.Op, c.Before)
	}
	wantAfter := map[string]any{"ID": json.Number("1"), "NAME": "a", "AMOUNT": json.Number("1.5"), "CREATED": "2026-10-11T07:30:18"}
	if !reflect.DeepEqual(c.After, wantAfter) {
		t.Fatalf("insert after = %#v, want %#v", c.After, wantAfter)
	}

	second := got[1]
	if len(second.Changes) != 2 {
		t.Fatalf("second transaction has %d changes, want 2", len(second.Changes))
	}
	u := second.Changes[0]
	if u.Op != hermod.OpUpdate {
		t.Fatalf("update op = %v", u.Op)
	}
	// The redo holds only what changed; the after-image is the before-image
	// with those columns replaced, so a sink can upsert the whole row.
	wantBefore := map[string]any{"ID": json.Number("1"), "NAME": "a", "AMOUNT": json.Number("1.5"), "CREATED": "2026-10-11T07:30:18"}
	wantUpdAfter := map[string]any{"ID": json.Number("1"), "NAME": "b", "AMOUNT": json.Number("1.5"), "CREATED": "2026-10-11T07:30:18"}
	if !reflect.DeepEqual(u.Before, wantBefore) || !reflect.DeepEqual(u.After, wantUpdAfter) {
		t.Fatalf("update before=%#v after=%#v", u.Before, u.After)
	}
	d := second.Changes[1]
	if d.Op != hermod.OpDelete || d.After != nil || d.Before["NAME"] != "b" {
		t.Fatalf("delete = %+v", d)
	}
}

func TestLogMinerProcessorNullIsNotAbsent(t *testing.T) {
	p := newLMProcessor(lmPosition{}, lmTypes)
	got := feed(t, p,
		upd(10, "X", "r1", 0, "A",
			map[string]lmValue{"NAME": lmNull},
			map[string]lmValue{"ID": v("7"), "NAME": v("x")}),
		commit(11, "X", "r2"),
	)
	after := got[0].Changes[0].After
	if val, ok := after["NAME"]; !ok || val != nil {
		t.Fatalf("a column set to NULL must be present and nil in the after-image; got %#v (present=%v)", val, ok)
	}
	if after["ID"] != json.Number("7") {
		t.Fatalf("an unchanged column keeps its before value; got %#v", after["ID"])
	}
}

func TestLogMinerProcessorDropsRolledBackTransactions(t *testing.T) {
	p := newLMProcessor(lmPosition{}, lmTypes)
	got := feed(t, p,
		ins(10, "X", "r1", 0, "A", map[string]lmValue{"ID": v("1")}),
		rollback(11, "X", "r2"),
		commit(12, "X", "r3"),
	)
	if len(got) != 0 {
		t.Fatalf("a rolled-back transaction must emit nothing; got %+v", got)
	}
}

func TestLogMinerProcessorUndoesPartialRollback(t *testing.T) {
	// ROLLBACK TO SAVEPOINT writes compensating records flagged ROLLBACK=1.
	// The statement they undo must not reach the pipeline.
	p := newLMProcessor(lmPosition{}, lmTypes)
	undone := del(12, "X", "r3", 0, "B", map[string]lmValue{"ID": v("2")})
	undone.Rollback = true
	got := feed(t, p,
		ins(10, "X", "r1", 0, "A", map[string]lmValue{"ID": v("1")}),
		ins(11, "X", "r2", 0, "B", map[string]lmValue{"ID": v("2")}),
		undone,
		commit(13, "X", "r4"),
	)
	if len(got) != 1 || len(got[0].Changes) != 1 || got[0].Changes[0].After["ID"] != json.Number("1") {
		t.Fatalf("want only the insert of ID 1; got %+v", got)
	}
}

func TestLogMinerProcessorIgnoresCommitsOfUntrackedTransactions(t *testing.T) {
	p := newLMProcessor(lmPosition{}, lmTypes)
	if got := feed(t, p, commit(5, "OTHER", "r1")); len(got) != 0 {
		t.Fatalf("a commit with no tracked changes emits nothing; got %+v", got)
	}
}

func TestLogMinerProcessorInterleavedTransactionsEmitInCommitOrder(t *testing.T) {
	p := newLMProcessor(lmPosition{}, lmTypes)
	got := feed(t, p,
		ins(10, "A", "r1", 0, "1", map[string]lmValue{"ID": v("1")}),
		ins(11, "B", "r2", 0, "2", map[string]lmValue{"ID": v("2")}),
		commit(12, "B", "r3"),
		commit(13, "A", "r4"),
	)
	if len(got) != 2 || got[0].XID != "B" || got[1].XID != "A" {
		t.Fatalf("want B then A; got %+v", got)
	}
	// When B is emitted, A is still open from SCN 10, so a restart that
	// resumes after B must still mine from 10 or A's insert is lost.
	if got[0].Low != 10 {
		t.Fatalf("low watermark at B's emission = %d, want 10", got[0].Low)
	}
	if got[1].Low != 13 {
		t.Fatalf("low watermark at A's emission = %d, want 13 (its commit)", got[1].Low)
	}
}

func TestLogMinerProcessorResumeSkipsAcknowledgedTransactions(t *testing.T) {
	// Resumed from: mining from SCN 10, transaction B committed at 12 already
	// delivered. A re-mine sees A and B again; only A may be emitted.
	p := newLMProcessor(lmPosition{Low: 10, CommitSCN: 12, CommitXIDs: []string{"B"}}, lmTypes)
	got := feed(t, p,
		ins(10, "A", "r1", 0, "1", map[string]lmValue{"ID": v("1")}),
		ins(11, "B", "r2", 0, "2", map[string]lmValue{"ID": v("2")}),
		commit(12, "B", "r3"),
		commit(13, "A", "r4"),
	)
	if len(got) != 1 || got[0].XID != "A" {
		t.Fatalf("want only A after resume; got %+v", got)
	}
}

func TestLogMinerProcessorResumeAtSameCommitSCNKeepsOtherTransactions(t *testing.T) {
	p := newLMProcessor(lmPosition{Low: 10, CommitSCN: 12, CommitXIDs: []string{"B"}}, lmTypes)
	got := feed(t, p,
		ins(11, "B", "r1", 0, "1", map[string]lmValue{"ID": v("1")}),
		ins(11, "C", "r2", 0, "2", map[string]lmValue{"ID": v("2")}),
		commit(12, "B", "r3"),
		commit(12, "C", "r4"),
	)
	if len(got) != 1 || got[0].XID != "C" {
		t.Fatalf("a transaction sharing the acknowledged commit SCN but not acknowledged must be emitted; got %+v", got)
	}
}

func TestLogMinerProcessorTrimForgetsUntrustedTailAndSkipsBoundaryOnRemine(t *testing.T) {
	p := newLMProcessor(lmPosition{}, lmTypes)
	// Window 1: X commits at 20. Y wrote at 20 and 25, still open. Anything
	// after the last commit is not yet known to be flushed, so the window is
	// trusted only up to 20.
	feed(t, p,
		ins(15, "X", "r1", 0, "1", map[string]lmValue{"ID": v("1")}),
		ins(20, "Y", "r2", 0, "2", map[string]lmValue{"ID": v("2")}),
		commit(20, "X", "r3"),
		ins(25, "Y", "r4", 0, "3", map[string]lmValue{"ID": v("3")}),
	)
	if b := p.trim(20); b != 20 {
		t.Fatalf("boundary = %d, want 20", b)
	}
	if low := p.low(); low != 20 {
		t.Fatalf("low after trim = %d, want 20 (Y's first change)", low)
	}

	// Window 2 re-reads from 20: the records at 20 already processed must be
	// skipped, the one at 25 re-read once.
	got := feed(t, p,
		ins(20, "Y", "r2", 0, "2", map[string]lmValue{"ID": v("2")}),
		commit(20, "X", "r3"),
		ins(25, "Y", "r4", 0, "3", map[string]lmValue{"ID": v("3")}),
		commit(30, "Y", "r5"),
	)
	if len(got) != 1 || got[0].XID != "Y" || len(got[0].Changes) != 2 {
		t.Fatalf("want Y with exactly 2 changes after the re-mine; got %+v", got)
	}
}

func TestLogMinerPositionStateRoundTrip(t *testing.T) {
	pos := lmPosition{Low: 42, CommitSCN: 50, CommitXIDs: []string{"A", "B"}}
	state := pos.state()
	back, ok := lmPositionFromState(state)
	if !ok || !reflect.DeepEqual(back, pos) {
		t.Fatalf("round trip: got %+v ok=%v from %v", back, ok, state)
	}
	if _, ok := lmPositionFromState(map[string]string{"last_id:T": "4"}); ok {
		t.Fatal("a polling cursor is not a LogMiner position")
	}
}

func TestDecodeMinedValue(t *testing.T) {
	cases := []struct {
		in   *string
		want lmValue
	}{
		{nil, lmValue{}},
		{strp("V"), lmValue{Present: true, Null: true}},
		{strp("Vabc"), lmValue{Present: true, Raw: "abc"}},
	}
	for _, c := range cases {
		if got := decodeMined(c.in); got != c.want {
			t.Errorf("decodeMined(%v) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestTypedValue(t *testing.T) {
	cases := []struct {
		typ, raw string
		want     any
	}{
		{"NUMBER", "42", json.Number("42")},
		{"NUMBER", "-1.25", json.Number("-1.25")},
		{"FLOAT", "1E+10", json.Number("1E+10")},
		{"BINARY_DOUBLE", "Nan", "Nan"},
		{"NUMBER", "not a number", "not a number"},
		{"VARCHAR2", "42", "42"},
		{"DATE", "2026-10-11T07:30:18", "2026-10-11T07:30:18"},
	}
	for _, c := range cases {
		if got := typedValue(c.typ, c.raw); got != c.want {
			t.Errorf("typedValue(%q, %q) = %#v, want %#v", c.typ, c.raw, got, c.want)
		}
	}
}

func strp(s string) *string { return &s }
