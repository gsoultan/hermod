// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package spool

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func rec(dev, raw string) Record {
	return Record{DeviceID: dev, Port: "/dev/ttyUSB0", ReceivedAt: 1700000000000000000, Raw: []byte(raw)}
}

func mustOpen(t *testing.T, dir string, opt Options) *Spool {
	t.Helper()
	s, err := Open(dir, opt)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestAppendAssignsIncreasingSequenceNumbers(t *testing.T) {
	s := mustOpen(t, t.TempDir(), Options{})
	got, err := s.Append([]Record{rec("a", "1"), rec("b", "2")})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Seq != 1 || got[1].Seq != 2 {
		t.Fatalf("seqs = %d,%d, want 1,2", got[0].Seq, got[1].Seq)
	}
	got, _ = s.Append([]Record{rec("a", "3")})
	if got[0].Seq != 3 {
		t.Fatalf("seq = %d, want 3", got[0].Seq)
	}
}

func TestReadFromReturnsRecordsAfterTheGivenSequence(t *testing.T) {
	s := mustOpen(t, t.TempDir(), Options{})
	for i := range 5 {
		if _, err := s.Append([]Record{rec("a", strconv.Itoa(i+1))}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ReadFrom(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Seq != 3 || string(got[1].Raw) != "4" {
		t.Fatalf("ReadFrom(2,2) = %+v", got)
	}
	if got[0].DeviceID != "a" || got[0].Port != "/dev/ttyUSB0" || got[0].ReceivedAt != 1700000000000000000 {
		t.Fatalf("fields not round-tripped: %+v", got[0])
	}
}

func TestRecordsSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append([]Record{rec("a", "x"), rec("a", "y")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Ack(1); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s = mustOpen(t, dir, Options{})
	st := s.Stats()
	if st.LastSeq != 2 || st.AckedSeq != 1 || st.Waiting != 1 {
		t.Fatalf("stats after reopen = %+v", st)
	}
	got, _ := s.ReadFrom(st.AckedSeq, 10)
	if len(got) != 1 || string(got[0].Raw) != "y" {
		t.Fatalf("unacked after reopen = %+v", got)
	}
	next, _ := s.Append([]Record{rec("a", "z")})
	if next[0].Seq != 3 {
		t.Fatalf("seq after reopen = %d, want 3", next[0].Seq)
	}
}

// A crash in the middle of a write leaves a partial record at the end of the
// active segment. Opening must drop that tail and keep everything before it,
// rather than refusing to start or returning garbage.
func TestOpenDropsATornTail(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, Options{})
	_, _ = s.Append([]Record{rec("a", "whole"), rec("a", "torn")})
	_ = s.Close()

	segs, _ := filepath.Glob(filepath.Join(dir, "*.seg"))
	if len(segs) != 1 {
		t.Fatalf("segments = %v", segs)
	}
	info, _ := os.Stat(segs[0])
	if err := os.Truncate(segs[0], info.Size()-3); err != nil {
		t.Fatal(err)
	}

	s = mustOpen(t, dir, Options{})
	if st := s.Stats(); st.LastSeq != 1 {
		t.Fatalf("LastSeq = %d, want 1", st.LastSeq)
	}
	next, _ := s.Append([]Record{rec("a", "after")})
	got, _ := s.ReadFrom(0, 10)
	if len(got) != 2 || string(got[1].Raw) != "after" || next[0].Seq != 2 {
		t.Fatalf("records = %+v", got)
	}
}

func TestAckDeletesWholeSegmentsOnly(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir, Options{SegmentBytes: 64})
	for range 10 {
		if _, err := s.Append([]Record{rec("a", "0123456789")}); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := filepath.Glob(filepath.Join(dir, "*.seg"))
	if len(before) < 3 {
		t.Fatalf("want several segments, got %d", len(before))
	}
	if err := s.Ack(10); err != nil {
		t.Fatal(err)
	}
	after, _ := filepath.Glob(filepath.Join(dir, "*.seg"))
	if len(after) != 1 {
		t.Fatalf("segments after acking everything = %d, want 1 (the active one)", len(after))
	}
	if st := s.Stats(); st.Waiting != 0 || st.OldestUnixNano != 0 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestAckBeyondLastSequenceIsRefused(t *testing.T) {
	s := mustOpen(t, t.TempDir(), Options{})
	_, _ = s.Append([]Record{rec("a", "1")})
	if err := s.Ack(5); err == nil {
		t.Fatal("Ack(5) with last seq 1 succeeded")
	}
}

// When the spool is full the oldest records go, and the loss is counted so
// the agent can report it instead of losing data silently.
func TestFullSpoolDropsOldestAndCountsThem(t *testing.T) {
	s := mustOpen(t, t.TempDir(), Options{SegmentBytes: 64, MaxBytes: 200})
	for i := range 20 {
		if _, err := s.Append([]Record{rec("a", fmt.Sprintf("%010d", i+1))}); err != nil {
			t.Fatal(err)
		}
	}
	st := s.Stats()
	if st.Bytes > 200+64 {
		t.Fatalf("spool holds %d bytes, cap 200", st.Bytes)
	}
	if st.Dropped == 0 {
		t.Fatal("nothing counted as dropped")
	}
	if st.AckedSeq != st.Dropped {
		t.Fatalf("dropped records must count as gone: acked %d, dropped %d", st.AckedSeq, st.Dropped)
	}
	got, _ := s.ReadFrom(st.AckedSeq, 100)
	if len(got) == 0 || got[len(got)-1].Seq != 20 || got[0].Seq != st.AckedSeq+1 {
		t.Fatalf("remaining = %d records, first %d", len(got), got[0].Seq)
	}
}

func TestStatsReportsOldestWaitingRecord(t *testing.T) {
	s := mustOpen(t, t.TempDir(), Options{})
	a := rec("a", "1")
	a.ReceivedAt = 100
	b := rec("a", "2")
	b.ReceivedAt = 200
	_, _ = s.Append([]Record{a, b})
	_ = s.Ack(1)
	if got := s.Stats().OldestUnixNano; got != 200 {
		t.Fatalf("OldestUnixNano = %d, want 200", got)
	}
}

func TestNotifySignalsAfterAppend(t *testing.T) {
	s := mustOpen(t, t.TempDir(), Options{})
	select {
	case <-s.Notify():
		t.Fatal("notified before any append")
	default:
	}
	_, _ = s.Append([]Record{rec("a", "1")})
	select {
	case <-s.Notify():
	default:
		t.Fatal("no notification after append")
	}
}

func TestSequentialReadsContinueWithoutRescanning(t *testing.T) {
	s := mustOpen(t, t.TempDir(), Options{SegmentBytes: 128})
	var want [][]byte
	for i := range 50 {
		raw := []byte(fmt.Sprintf("r%02d", i+1))
		want = append(want, raw)
		_, _ = s.Append([]Record{{DeviceID: "d", Raw: raw}})
	}
	var got [][]byte
	after := uint64(0)
	for {
		batch, err := s.ReadFrom(after, 7)
		if err != nil {
			t.Fatal(err)
		}
		if len(batch) == 0 {
			break
		}
		for _, r := range batch {
			got = append(got, r.Raw)
		}
		after = batch[len(batch)-1].Seq
	}
	if len(got) != len(want) {
		t.Fatalf("read %d records, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("record %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// A crash after the acknowledgement is saved but before the segments it
// covers are deleted must not leave those segments behind for ever.
func TestOpenRemovesSegmentsAlreadyAcknowledged(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, Options{SegmentBytes: 64})
	for range 10 {
		_, _ = s.Append([]Record{rec("a", "0123456789")})
	}
	_ = s.Close()
	if err := saveState(dir, state{Acked: 10}); err != nil {
		t.Fatal(err)
	}
	s = mustOpen(t, dir, Options{SegmentBytes: 64})
	segs, _ := filepath.Glob(filepath.Join(dir, "*.seg"))
	if len(segs) != 1 {
		t.Fatalf("segments = %d, want 1", len(segs))
	}
	if st := s.Stats(); st.Waiting != 0 || st.LastSeq != 10 {
		t.Fatalf("stats = %+v", st)
	}
}
