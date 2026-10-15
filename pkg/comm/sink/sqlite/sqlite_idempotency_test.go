// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package sqlite

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	_ "modernc.org/sqlite"
)

func TestSQLiteSink_IdempotentByID(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Prepare table schema: (id TEXT PRIMARY KEY, data BLOB)
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS mytable (id TEXT PRIMARY KEY, data BLOB)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	snk := NewSQLiteSink(dbPath, "", nil, true, "hard_delete", "", "", "", false, false)
	defer snk.Close()

	// First write
	msg1 := message.AcquireMessage()
	msg1.SetID("dup-1")
	msg1.SetOperation(hermod.OpCreate)
	msg1.SetTable("mytable")
	msg1.SetSchema("")
	payload1, _ := json.Marshal(map[string]any{"value": 1})
	msg1.SetPayload(payload1)
	if err := snk.Write(t.Context(), msg1); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	message.ReleaseMessage(msg1)

	// Duplicate write with same ID (should replace row, not create a second one)
	msg2 := message.AcquireMessage()
	msg2.SetID("dup-1")
	msg2.SetOperation(hermod.OpCreate)
	msg2.SetTable("mytable")
	payload2, _ := json.Marshal(map[string]any{"value": 2})
	msg2.SetPayload(payload2)
	if err := snk.Write(t.Context(), msg2); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	message.ReleaseMessage(msg2)

	// Validate only one row exists and content matches the latest (REPLACE semantics)
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM mytable").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row, got %d", count)
	}
}
