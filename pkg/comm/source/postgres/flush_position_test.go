package postgres

import (
	"testing"

	"github.com/jackc/pglogrepl"
)

// The flush position reported to Postgres is what advances the slot's
// confirmed_flush_lsn, and therefore what lets the server release WAL.
//
// Reporting only lastAckedLSN means the slot never moves for WAL this pipeline
// received and deliberately did not deliver — a table outside the publication,
// or traffic in another database on the same instance. Retention then grows
// without limit on someone else's primary, which with the default
// max_slot_wal_keep_size of -1 ends as a full disk rather than an error.
//
// The rule below is the whole of the safety argument, and the first test is the
// one that matters: a position is only confirmed once everything delivered from
// below it has been acknowledged.
func TestFlushPositionNeverOutrunsUnacknowledgedWork(t *testing.T) {
	t.Run("delivered but un-acknowledged work pins the flush position", func(t *testing.T) {
		p := NewPostgresSource("postgres://u:p@localhost:5432/db", "slot", "pub", nil, true, "", 0)

		// Received far ahead, delivered up to 500, acknowledged only to 200.
		p.mu.Lock()
		p.lastReceivedLSN = 9000
		p.lastAckedLSN = 200
		p.mu.Unlock()
		p.lastEmittedLSN.Store(500)

		got := p.flushPosition()
		if got != 200 {
			t.Fatalf("flush position = %d, want 200: confirming past un-acknowledged messages tells Postgres it may discard WAL this pipeline still owes, and those changes can never be replayed",
				got)
		}
	})

	t.Run("WAL that was never delivered can be confirmed", func(t *testing.T) {
		p := NewPostgresSource("postgres://u:p@localhost:5432/db", "slot", "pub", nil, true, "", 0)

		// Everything ever delivered (up to 500) is acknowledged. The gap from
		// 500 to 9000 is WAL this workflow was never given: other tables, other
		// databases, autovacuum.
		p.mu.Lock()
		p.lastReceivedLSN = 9000
		p.lastAckedLSN = 500
		p.mu.Unlock()
		p.lastEmittedLSN.Store(500)

		got := p.flushPosition()
		if got != 9000 {
			t.Errorf("flush position = %d, want 9000: WAL the pipeline was never given is retained forever, so an idle workflow on a busy server grows the source's disk without limit",
				got)
		}
	})

	t.Run("a source that has delivered nothing at all can still confirm", func(t *testing.T) {
		p := NewPostgresSource("postgres://u:p@localhost:5432/db", "slot", "pub", nil, true, "", 0)
		p.mu.Lock()
		p.lastReceivedLSN = 4096
		p.lastAckedLSN = 0
		p.mu.Unlock()

		if got := p.flushPosition(); got != 4096 {
			t.Errorf("flush position = %d, want 4096: a workflow whose tables are quiet would retain WAL forever", got)
		}
	})

	t.Run("the flush position never moves backwards", func(t *testing.T) {
		p := NewPostgresSource("postgres://u:p@localhost:5432/db", "slot", "pub", nil, true, "", 0)
		p.mu.Lock()
		p.lastReceivedLSN = 100
		p.lastAckedLSN = 800
		p.mu.Unlock()
		p.lastEmittedLSN.Store(800)

		if got := p.flushPosition(); got < 800 {
			t.Errorf("flush position = %d, want at least the acknowledged 800", got)
		}
	})

	t.Run("acknowledging the last delivered change releases the received gap", func(t *testing.T) {
		p := NewPostgresSource("postgres://u:p@localhost:5432/db", "slot", "pub", nil, true, "", 0)
		p.mu.Lock()
		p.lastReceivedLSN = 9000
		p.lastAckedLSN = 200
		p.mu.Unlock()
		p.lastEmittedLSN.Store(500)

		if got := p.flushPosition(); got != 200 {
			t.Fatalf("precondition: flush position = %d, want 200", got)
		}

		// The outstanding message is acknowledged.
		p.mu.Lock()
		p.lastAckedLSN = 500
		p.mu.Unlock()

		if got := p.flushPosition(); got != 9000 {
			t.Errorf("flush position = %d, want 9000 once nothing is outstanding", got)
		}
	})

	t.Run("a non-CDC source reports the acknowledged position unchanged", func(t *testing.T) {
		p := NewPostgresSource("postgres://u:p@localhost:5432/db", "", "", nil, false, "", 0)
		p.mu.Lock()
		p.lastReceivedLSN = 9000
		p.lastAckedLSN = 100
		p.mu.Unlock()

		if got := p.flushPosition(); got != 100 {
			t.Errorf("flush position = %d, want 100 for a non-CDC source", got)
		}
	})
}

// PendingWork and the flush position answer the same question and must never
// disagree: if the source says it is owed acknowledgements, it must not be
// confirming past them.
func TestFlushPositionAgreesWithPendingWork(t *testing.T) {
	cases := []struct {
		name                     string
		received, emitted, acked pglogrepl.LSN
	}{
		{"nothing delivered", 9000, 0, 0},
		{"all delivered work acknowledged", 9000, 500, 500},
		{"delivery outstanding", 9000, 500, 200},
		{"nothing received yet", 0, 0, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewPostgresSource("postgres://u:p@localhost:5432/db", "slot", "pub", nil, true, "", 0)
			p.mu.Lock()
			p.lastReceivedLSN = tc.received
			p.lastAckedLSN = tc.acked
			p.mu.Unlock()
			p.lastEmittedLSN.Store(uint64(tc.emitted))

			pending, _ := p.PendingWork()
			flush := p.flushPosition()

			if pending && flush > tc.acked {
				t.Errorf("source reports outstanding work but confirms position %d past the acknowledged %d",
					flush, tc.acked)
			}
		})
	}
}
