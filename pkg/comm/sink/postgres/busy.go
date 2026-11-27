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

	"github.com/gsoultan/hermod"
	"github.com/jackc/pgx/v5/pgconn"
)

// busyStates are the SQLSTATEs that mean the server is up and has no room for
// this client right now, so waiting is what fixes them.
var busyStates = map[string]bool{
	"53300": true, // too_many_connections
	"57P03": true, // cannot_connect_now
}

// markBusy marks err as busy (hermod.ErrBusy) when the server said it has no
// room, so the engine waits instead of spending retries. Other errors, and
// nil, are returned as they are.
func markBusy(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && busyStates[pgErr.Code] {
		return &hermod.Busy{Err: err}
	}
	return err
}
