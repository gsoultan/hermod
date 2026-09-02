package postgres

import (
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/jackc/pglogrepl"
)

// Replication lag and outstanding work are different quantities, and the stall
// watchdog needs the second one.
//
// GetLag compares pg_current_wal_lsn() — which advances on every write anywhere
// on the server instance — against the slot's confirmed_flush_lsn, which only
// advances when this source acknowledges a message it actually delivered
// (Ack, postgres.go). An idle workflow on a busy server therefore reports lag
// that grows forever. PendingWork answers the question that actually matters:
// did this source hand something over that never came back acknowledged?
func TestPendingWorkTracksDeliveredButUnacknowledgedChanges(t *testing.T) {
	newSource := func() *PostgresSource {
		return NewPostgresSource("postgres://user:pass@localhost:5432/db", "slot", "pub", nil, true, "", 0)
	}

	t.Run("a source that has delivered nothing owes nothing", func(t *testing.T) {
		s := newSource()
		pending, known := s.PendingWork()
		if !known {
			t.Fatal("a CDC source should be able to answer whether it is owed acknowledgements")
		}
		if pending {
			t.Error("a source that has delivered nothing reported outstanding work; an idle workflow would be restarted")
		}
	})

	t.Run("a delivered change is outstanding until it is acknowledged", func(t *testing.T) {
		s := newSource()
		msg := s.handleInsert(4096, &pglogrepl.InsertMessage{})
		if msg == nil {
			t.Skip("handleInsert filtered the change; nothing to dispatch")
		}

		if err := s.dispatch(t.Context(), 4096, msg); err != nil {
			t.Fatalf("dispatch: %v", err)
		}

		if pending, _ := s.PendingWork(); !pending {
			t.Fatal("a change handed to the pipeline was not counted as outstanding; a real wedge would look idle")
		}

		if err := s.Ack(t.Context(), msg); err != nil {
			t.Fatalf("Ack: %v", err)
		}
		if pending, _ := s.PendingWork(); pending {
			t.Error("an acknowledged change is still counted as outstanding; a healthy workflow would be restarted")
		}
	})

	t.Run("WAL that was received but never delivered is not outstanding work", func(t *testing.T) {
		s := newSource()
		// A keepalive advances the received position without delivering
		// anything — the same shape as traffic on tables this workflow does not
		// follow, or on another database on the same server.
		s.mu.Lock()
		s.lastReceivedLSN = 1 << 30
		s.mu.Unlock()

		if pending, _ := s.PendingWork(); pending {
			t.Error("WAL the pipeline was never given was counted as work it owes; every idle workflow on a busy server would be restarted in a loop")
		}
	})

	t.Run("a non-CDC source declines to answer rather than claiming nothing is pending", func(t *testing.T) {
		s := NewPostgresSource("postgres://user:pass@localhost:5432/db", "", "", nil, false, "", 0)
		if _, known := s.PendingWork(); known {
			t.Error("a non-CDC source claimed authority over a question it cannot answer, suppressing the caller's own fallback")
		}
	})

	t.Run("the source advertises the interface the watchdog looks for", func(t *testing.T) {
		var s any = newSource()
		if _, ok := s.(hermod.PendingWorkReporter); !ok {
			t.Fatal("PostgresSource no longer implements hermod.PendingWorkReporter")
		}
	})
}
