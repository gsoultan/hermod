package postgres

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
)

func TestPostgresSource_DefaultSlotAndPublication(t *testing.T) {
	tests := []struct {
		name        string
		slot        string
		publication string
		wantSlot    string
		wantPub     string
	}{
		{
			name:        "empty falls back to defaults",
			slot:        "",
			publication: "",
			wantSlot:    defaultSlotName,
			wantPub:     defaultPublicationName,
		},
		{
			name:        "whitespace falls back to defaults",
			slot:        "   ",
			publication: "\t",
			wantSlot:    defaultSlotName,
			wantPub:     defaultPublicationName,
		},
		{
			name:        "user input is preserved",
			slot:        "my_slot",
			publication: "my_pub",
			wantSlot:    "my_slot",
			wantPub:     "my_pub",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewPostgresSource("postgres://user:pass@localhost:5432/db", tt.slot, tt.publication, nil, true)
			if s.slotName != tt.wantSlot {
				t.Errorf("slotName = %q, want %q", s.slotName, tt.wantSlot)
			}
			if s.publicationName != tt.wantPub {
				t.Errorf("publicationName = %q, want %q", s.publicationName, tt.wantPub)
			}
		})
	}
}

func TestPostgresSource_CloseUninitializedIsSafeAndIdempotent(t *testing.T) {
	// Lightweight operations (test connection, fetch tables/databases, etc.)
	// open the metadata connection without marking the source initialized.
	// Close must still release that connection (and reset state) so repeated
	// requests do not leak connections and take the worker offline. It must
	// also be safe to call multiple times.
	s := NewPostgresSource("postgres://user:pass@localhost:5432/db", "", "", nil, false)

	if err := s.Close(); err != nil {
		t.Fatalf("first Close on uninitialized source: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close on uninitialized source: %v", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		t.Errorf("metadata connection not released after Close: got %v", s.conn)
	}
	if s.replConn != nil {
		t.Errorf("replication connection not released after Close: got %v", s.replConn)
	}
	if s.initialized {
		t.Error("source still marked initialized after Close")
	}
}

// buildKeepalive constructs a Primary Keepalive Message CopyData payload.
// Layout (after the 'k' byte): ServerWALEnd (8) | ServerTime (8) | ReplyRequested (1).
func buildKeepalive(walEnd pglogrepl.LSN, replyRequested bool) []byte {
	data := make([]byte, 1+8+8+1)
	data[0] = pglogrepl.PrimaryKeepaliveMessageByteID
	binary.BigEndian.PutUint64(data[1:9], uint64(walEnd))
	binary.BigEndian.PutUint64(data[9:17], uint64(time.Now().UnixMicro()))
	if replyRequested {
		data[17] = 1
	}
	return data
}

func TestPostgresSource_HandleCopyData_KeepaliveAdvancesLSN(t *testing.T) {
	tests := []struct {
		name    string
		start   pglogrepl.LSN
		walEnd  pglogrepl.LSN
		wantLSN pglogrepl.LSN
	}{
		{"advances on higher WAL end", 100, 200, 200},
		{"does not regress on lower WAL end", 300, 200, 300},
		{"keeps value on equal WAL end", 150, 150, 150},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewPostgresSource("postgres://user:pass@localhost:5432/db", "slot", "pub", nil, true)
			s.lastReceivedLSN = tc.start

			// ReplyRequested=false so no replication connection is needed.
			if err := s.handleCopyData(t.Context(), nil, buildKeepalive(tc.walEnd, false)); err != nil {
				t.Fatalf("handleCopyData returned error: %v", err)
			}

			s.mu.Lock()
			got := s.lastReceivedLSN
			s.mu.Unlock()
			if got != tc.wantLSN {
				t.Errorf("lastReceivedLSN = %d, want %d", got, tc.wantLSN)
			}
		})
	}
}

func TestPostgresSource_HandleCopyData_EmptyIsSafe(t *testing.T) {
	s := NewPostgresSource("postgres://user:pass@localhost:5432/db", "slot", "pub", nil, true)
	if err := s.handleCopyData(t.Context(), nil, nil); err != nil {
		t.Fatalf("handleCopyData(nil) returned error: %v", err)
	}
}

func TestPostgresSource_HandleReplicationMessage_ErrorResponse(t *testing.T) {
	s := NewPostgresSource("postgres://user:pass@localhost:5432/db", "slot", "pub", nil, true)
	err := s.handleReplicationMessage(t.Context(), nil, &pgproto3.ErrorResponse{Message: "boom"})
	if err == nil {
		t.Fatal("expected error for ErrorResponse, got nil")
	}
}

func TestPostgresSource_Dispatch_DeliversMessage(t *testing.T) {
	s := NewPostgresSource("postgres://user:pass@localhost:5432/db", "slot", "pub", nil, true)
	msg := s.handleInsert(42, &pglogrepl.InsertMessage{})

	if err := s.dispatch(t.Context(), msg); err != nil {
		t.Fatalf("dispatch returned error: %v", err)
	}
	select {
	case got := <-s.msgChan:
		if got != msg {
			t.Errorf("dispatched message mismatch")
		}
	default:
		t.Fatal("expected message on msgChan, found none")
	}
}

func TestPostgresSource_Dispatch_RespectsCancellation(t *testing.T) {
	s := NewPostgresSource("postgres://user:pass@localhost:5432/db", "slot", "pub", nil, true)
	// Fill the buffered channel so dispatch must block, then cancel.
	for i := range cap(s.msgChan) {
		s.msgChan <- s.handleInsert(pglogrepl.LSN(i), &pglogrepl.InsertMessage{})
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := s.dispatch(ctx, s.handleInsert(1, &pglogrepl.InsertMessage{}))
	if err == nil {
		t.Fatal("expected context error when channel is full and ctx cancelled")
	}
}

func TestIsSlotActiveError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"sqlstate 55006", &pgconn.PgError{Code: "55006", Message: "boom"}, true},
		{"message is active for pid", errors.New(`replication slot "hermod_slot" is active for PID 1234`), true},
		{"message already active", errors.New("replication slot already active"), true},
		{"unrelated pg error", &pgconn.PgError{Code: "42601", Message: "syntax error"}, false},
		{"unrelated error", errors.New("connection refused"), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSlotActiveError(tc.err); got != tc.want {
				t.Errorf("isSlotActiveError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestPostgresSource_Read(t *testing.T) {
	// Skip test if no postgres is running
	t.Skip("Skipping test that requires a running Postgres instance")
	s := NewPostgresSource("postgres://user:pass@localhost:5432/db", "test_slot", "test_pub", nil, true)
	defer s.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	_, err := s.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read from PostgresSource: %v", err)
	}
}

// noopLogger is a minimal hermod.Logger used to exercise the logging path.
type noopLogger struct{}

func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}

// TestLogDoesNotDeadlockWhileHoldingMu guards against a regression where log()
// acquired the same non-reentrant mutex (mu) that init(), Close() and every
// *Locked helper already hold when they log. That self-deadlock silently froze
// the streaming goroutine, so the source appeared "online" but never delivered
// CDC changes. log() must use its own lock (logMu) and therefore be safe to
// call while mu is held.
func TestLogDoesNotDeadlockWhileHoldingMu(t *testing.T) {
	s := NewPostgresSource("postgres://localhost/db", "slot", "pub", nil, true)
	s.SetLogger(noopLogger{})

	done := make(chan struct{})
	go func() {
		// Reproduce the exact call pattern of init()/Close()/*Locked helpers:
		// hold mu, then log.
		s.mu.Lock()
		s.log("INFO", "logging while holding mu must not deadlock", "k", "v")
		s.mu.Unlock()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("log() deadlocked while mu was held; logging must not acquire mu")
	}
}
