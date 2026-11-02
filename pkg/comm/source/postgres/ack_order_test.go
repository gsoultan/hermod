// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package postgres

import (
	"testing"

	"github.com/jackc/pglogrepl"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

func ackLSN(t *testing.T, p *PostgresSource, lsn pglogrepl.LSN) {
	t.Helper()
	m := message.AcquireMessage()
	defer message.ReleaseMessage(m)
	m.SetMetadata("lsn", lsn.String())
	if err := p.Ack(t.Context(), m); err != nil {
		t.Fatalf("ack %s: %v", lsn, err)
	}
}

// Acknowledgements arrive out of order: the engine runs changes on a pool of
// workers, so a later change routinely finishes before an earlier one.
//
// Ack kept the highest position acknowledged, so one fast message confirmed
// every slower one below it to the slot. A stop or a crash in that window, and
// those changes were gone: Postgres had been told they were dealt with and
// never sent them again. The branching load test lost 30 of 3,000 Postgres
// records across one restart this way, with nothing logged.
//
// The position that may be confirmed is the highest one below which every
// delivered change has been acknowledged.
func TestAcknowledgedPositionIsTheContiguousLowWaterMark(t *testing.T) {
	p := NewPostgresSource("postgres://u:p@localhost:5432/db", "slot", "pub", nil, true, "", 0)
	p.mu.Lock()
	p.lastReceivedLSN = 9000
	p.mu.Unlock()

	for _, lsn := range []pglogrepl.LSN{100, 200, 300} {
		p.noteEmitted(lsn)
	}

	ackLSN(t, p, 300)
	if got := p.flushPosition(); got >= 100 {
		t.Fatalf("after acknowledging only 300, flush position = %d, want below 100: "+
			"100 and 200 are still in flight and would never be replayed", got)
	}
	if pending, _ := p.PendingWork(); !pending {
		t.Fatal("PendingWork = false with 100 and 200 unacknowledged")
	}

	ackLSN(t, p, 100)
	if got := p.flushPosition(); got < 100 || got >= 200 {
		t.Fatalf("after acknowledging 300 and 100, flush position = %d, want at least 100 and below the outstanding 200", got)
	}

	ackLSN(t, p, 200)
	if got := p.flushPosition(); got != 9000 {
		t.Fatalf("with every delivered change acknowledged, flush position = %d, want the received 9000", got)
	}
	if pending, _ := p.PendingWork(); pending {
		t.Fatal("PendingWork = true with everything acknowledged")
	}
}

// The consumer can acknowledge a change before dispatch has recorded handing
// it over: the channel send happens first. That ordering must not leave the
// position pinned behind a change that is long done.
func TestAcknowledgementBeforeEmissionIsRecorded(t *testing.T) {
	p := NewPostgresSource("postgres://u:p@localhost:5432/db", "slot", "pub", nil, true, "", 0)
	ackLSN(t, p, 100)
	p.noteEmitted(100)
	if pending, _ := p.PendingWork(); pending {
		t.Fatal("PendingWork = true although 100 was acknowledged")
	}
	if got := p.flushPosition(); got != 100 {
		t.Fatalf("flush position = %d, want 100", got)
	}
}

// A change that is never acknowledged holds the position, as it should. It
// must not also hold memory for every change acknowledged behind it: on a CDC
// stream that is the whole stream, for as long as the workflow runs.
func TestAckWindowBehindAStuckChangeStaysSmall(t *testing.T) {
	var w ackWindow
	w.emit(1) // never acknowledged
	for lsn := pglogrepl.LSN(2); lsn < 200_000; lsn++ {
		w.emit(lsn)
		w.ack(lsn)
	}
	if w.safe != 0 {
		t.Fatalf("safe = %d, want 0: change 1 is still outstanding", w.safe)
	}
	if n := len(w.low) + len(w.outstanding) + len(w.early); n > 4096 {
		t.Fatalf("the window holds %d entries for one outstanding change", n)
	}
	w.ack(1)
	if w.safe != 199_999 {
		t.Fatalf("safe = %d once everything is acknowledged, want 199999", w.safe)
	}
}
