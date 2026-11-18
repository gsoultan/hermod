// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package advanced

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// --- "Set a timeout on the step", on a step that had none ------------------
//
// The advisor tells a user whose Exec SQL step has stopped finishing messages
// to set a timeout on it, and the step had no such setting. Whatever it waited
// on -- the pool, a connection whose peer had gone, a lock -- it waited for as
// long as the message's context allowed, which was forever.

// stalledRegistry never hands out a database: it waits until the caller gives up.
type stalledRegistry struct{}

func (stalledRegistry) GetOrOpenDBByID(ctx context.Context, _ string) (*sql.DB, string, error) {
	<-ctx.Done()
	return nil, "", ctx.Err()
}

func TestExecuteSQLTimeoutEndsAStalledStep(t *testing.T) {
	tr, _ := newExecSQLFixture(t)
	ctx := context.WithValue(t.Context(), hermod.RegistryKey, stalledRegistry{})

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)

	done := make(chan error, 1)
	go func() {
		_, err := tr.Transform(ctx, msg, map[string]any{
			"sourceId":      "src1",
			"queryTemplate": "DELETE FROM audit",
			"timeout":       "100ms",
		})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a step that never got its database reported success")
		}
		if !strings.Contains(err.Error(), "100ms") {
			t.Errorf("the error does not say the step ran out of its time: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a step with a 100ms timeout was still waiting after 5s")
	}
}

func TestExecuteSQLRejectsATimeoutThatIsNotADuration(t *testing.T) {
	tr, reg := newExecSQLFixture(t)
	for _, bad := range []string{"30", "soon", "-1s", "0s"} {
		_, err := runExecSQL(t, tr, reg, map[string]any{
			"sourceId":      "src1",
			"queryTemplate": "INSERT INTO audit VALUES ('a','b')",
			"timeout":       bad,
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "timeout") {
			t.Errorf("timeout %q: got %v, want an error naming the timeout", bad, err)
		}
	}
	if n := countAudit(t, reg); n != 0 {
		t.Errorf("a node with a bad timeout still wrote %d row(s)", n)
	}
}

func TestExecuteSQLTimeoutLeavesAQuickStatementAlone(t *testing.T) {
	tr, reg := newExecSQLFixture(t)
	if _, err := runExecSQL(t, tr, reg, map[string]any{
		"sourceId":      "src1",
		"queryTemplate": "INSERT INTO audit VALUES ('a','b')",
		"timeout":       "5s",
	}, nil); err != nil {
		t.Fatal(err)
	}
	if n := countAudit(t, reg); n != 1 {
		t.Errorf("wrote %d row(s), want 1", n)
	}
}
