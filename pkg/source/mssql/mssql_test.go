package mssql

import (
	"testing"

	"github.com/user/hermod"
)

func TestMSSQLSource_MapToMessage(t *testing.T) {
	m := NewMSSQLSource("test-conn", []string{"dbo.users"}, false, true)

	lsn := []byte{0x01, 0x02, 0x03}
	seq := []byte{0x00, 0x00, 0x01}
	data := map[string]interface{}{
		"id":   1,
		"name": "John",
	}

	// Test Insert (op 2)
	msg := m.mapToMessage("dbo.users", 2, lsn, seq, data)
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
	if msg.Metadata()["seqval"] != "000001" {
		t.Errorf("expected seqval 000001, got %s", msg.Metadata()["seqval"])
	}

	// Test Delete (op 1)
	msg = m.mapToMessage("dbo.users", 1, lsn, seq, data)
	if msg.Operation() != hermod.OpDelete {
		t.Errorf("expected OpDelete, got %v", msg.Operation())
	}

	// Test Update After (op 4)
	msg = m.mapToMessage("dbo.users", 4, lsn, seq, data)
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

func TestMSSQLSource_New(t *testing.T) {
	m := NewMSSQLSource("test-conn", []string{"dbo.users", "orders"}, true, true)
	if m.connString != "test-conn" {
		t.Errorf("expected connString test-conn, got %s", m.connString)
	}
	if len(m.tables) != 2 {
		t.Errorf("expected 2 tables, got %d", len(m.tables))
	}
	if m.tables[0] != "dbo.users" {
		t.Errorf("expected table dbo.users, got %s", m.tables[0])
	}
	if m.tables[1] != "orders" {
		t.Errorf("expected table orders, got %s", m.tables[1])
	}
	if !m.autoEnableCDC {
		t.Error("expected autoEnableCDC true")
	}
	if !m.useCDC {
		t.Error("expected useCDC true")
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
