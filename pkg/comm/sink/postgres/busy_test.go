// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package postgres

import (
	"errors"
	"fmt"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/jackc/pgx/v5/pgconn"
)

// A server out of connection slots is up and has no room: the engine waits
// that out (pkg/engine/busy.go) when the sink says so. Classified by SQLSTATE,
// never by message text, which the server translates.
func TestServerWithNoRoomIsReportedAsBusy(t *testing.T) {
	cases := []struct {
		code string
		busy bool
	}{
		{"53300", true},  // too_many_connections
		{"57P03", true},  // cannot_connect_now: starting up, or a standby catching up
		{"53200", false}, // out_of_memory: not something waiting fixes reliably
		{"23505", false}, // unique_violation
		{"42P01", false}, // undefined_table
	}
	for _, tc := range cases {
		err := markBusy(fmt.Errorf("write: %w", &pgconn.PgError{Code: tc.code, Message: "translated text"}))
		if got := errors.Is(err, hermod.ErrBusy); got != tc.busy {
			t.Errorf("SQLSTATE %s: busy = %v, want %v", tc.code, got, tc.busy)
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != tc.code {
			t.Errorf("SQLSTATE %s: the server's error is no longer reachable from %v", tc.code, err)
		}
	}
	if markBusy(nil) != nil {
		t.Error("markBusy(nil) is not nil")
	}
}
