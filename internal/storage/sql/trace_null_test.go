// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package sql

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestGetMessageTrace_NullValues(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	s := NewSQLStorage(db, "sqlite").(*sqlStorage)

	ctx := context.Background()
	if err := s.Init(ctx); err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}

	workflowID := uuid.New().String()
	messageID := uuid.New().String()
	traceID := uuid.New().String()

	// Insert a trace step with NULL values for before_data, after_data, and error
	_, err = db.ExecContext(ctx, `INSERT INTO message_trace_steps (message_id, workflow_id, node_id, timestamp, duration_ms, after_data, error)
		VALUES (?, ?, ?, ?, ?, NULL, NULL)`,
		messageID, workflowID, "node-1", time.Now(), 100)
	_ = traceID
	if err != nil {
		t.Fatalf("failed to insert trace step: %v", err)
	}

	// This should fail before the fix
	trace, err := s.GetMessageTrace(ctx, workflowID, messageID)
	if err != nil {
		t.Errorf("GetMessageTrace failed: %v", err)
	}

	if len(trace.Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(trace.Steps))
	}
}
