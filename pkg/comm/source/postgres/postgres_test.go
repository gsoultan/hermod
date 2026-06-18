package postgres

import (
	"context"
	"testing"
	"time"
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
