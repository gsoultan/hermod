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
