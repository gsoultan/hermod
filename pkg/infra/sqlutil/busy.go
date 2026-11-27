// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package sqlutil

import (
	"errors"

	"github.com/gsoultan/hermod"
)

// busyStates are the SQLSTATEs that mean the server is up and has no room for
// this client right now, so waiting is what fixes them. Both are sent before
// any statement runs, so repeating the attempt is safe even for a write.
var busyStates = map[string]bool{
	"53300": true, // too_many_connections
	"57P03": true, // cannot_connect_now
}

// sqlStater is an error that carries a SQLSTATE, as pgx's *pgconn.PgError
// does. Reading it through an interface keeps this package driver-free.
type sqlStater interface{ SQLState() string }

// MarkBusy marks err as busy (hermod.ErrBusy) when the server refused it for
// lack of room, read from the SQLSTATE and never from the message text, which
// a server localises. Other errors, and nil, are returned as they are.
func MarkBusy(err error) error {
	var s sqlStater
	if errors.As(err, &s) && busyStates[s.SQLState()] {
		return &hermod.Busy{Err: err}
	}
	return err
}
