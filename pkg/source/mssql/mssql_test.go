package mssql

import (
	"testing"

	"github.com/user/hermod"
)

func TestMSSQLSource_MapToMessage(t *testing.T) {
	m := NewMSSQLSource("test-conn", []string{"dbo.users"}, false)

	lsn := []byte{0x01, 0x02, 0x03}
	data := map[string]interface{}{
		"id":   1,
		"name": "John",
	}

	// Test Insert (op 2)
	msg := m.mapToMessage("dbo.users", 2, lsn, data)
	if msg.Operation() != hermod.OpCreate {
		t.Errorf("expected OpCreate, got %v", msg.Operation())
	}
	if msg.Table() != "users" {
		t.Errorf("expected table users, got %s", msg.Table())
	}
	if msg.Schema() != "dbo" {
		t.Errorf("expected schema dbo, got %s", msg.Schema())
	}
	if msg.Metadata()["lsn"] != "010203" {
		t.Errorf("expected lsn 010203, got %s", msg.Metadata()["lsn"])
	}

	// Test Delete (op 1)
	msg = m.mapToMessage("dbo.users", 1, lsn, data)
	if msg.Operation() != hermod.OpDelete {
		t.Errorf("expected OpDelete, got %v", msg.Operation())
	}

	// Test Update After (op 4)
	msg = m.mapToMessage("dbo.users", 4, lsn, data)
	if msg.Operation() != hermod.OpUpdate {
		t.Errorf("expected OpUpdate, got %v", msg.Operation())
	}
}

func TestMSSQLSource_MatchTable(t *testing.T) {
	m := &MSSQLSource{}
	tests := []struct {
		configured     string
		physicalSchema string
		physicalTable  string
		expected       bool
	}{
		{"users", "dbo", "users", true},
		{"dbo.users", "dbo", "users", true},
		{"[dbo].[users]", "dbo", "users", true},
		{"SalesDB.dbo.users", "dbo", "users", true},
		{"other.users", "dbo", "users", false},
		{"users", "app", "users", true},
		{"app.users", "app", "users", true},
		{"dbo.users", "app", "users", false},
	}

	for _, tt := range tests {
		got := m.matchTable(tt.configured, tt.physicalSchema, tt.physicalTable)
		if got != tt.expected {
			t.Errorf("matchTable(%q, %q, %q) = %v; want %v", tt.configured, tt.physicalSchema, tt.physicalTable, got, tt.expected)
		}
	}
}

func TestNormalizeTableName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"dbo.users", "dbo.users"},
		{"[dbo].[users]", "dbo.users"},
		{"[dbo].users", "dbo.users"},
		{"dbo.[users]", "dbo.users"},
		{"users", "users"},
		{"[users]", "users"},
		{" [dbo].[users] ", "dbo.users"},
	}

	for _, tt := range tests {
		got := normalizeTableName(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeTableName(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}
