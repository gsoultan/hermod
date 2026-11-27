// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package lookup

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/jackc/pgx/v5/pgconn"
)

// A lookup that ran out of time while every one of Hermod's connections to
// the source was in use never reached the database: it queued inside Hermod
// for a connection. That is a busy database, not a failed query, so it is
// reported as busy, and the engine waits it out instead of failing the
// message. A lookup that had a connection and still ran out of time is an
// ordinary failure: the query itself was slow.
func TestALookupThatQueuedForAConnectionIsBusy(t *testing.T) {
	err := fmt.Errorf("query: %w", context.DeadlineExceeded)

	full := poolUse{inUse: 10, max: 10}.explain(err)
	if !errors.Is(full, hermod.ErrBusy) {
		t.Errorf("all connections in use: %v is not busy", full)
	}
	if !errors.Is(full, context.DeadlineExceeded) {
		t.Errorf("all connections in use: %v lost the deadline it wraps", full)
	}

	room := poolUse{inUse: 3, max: 10}.explain(err)
	if errors.Is(room, hermod.ErrBusy) {
		t.Errorf("a connection was free, so the query was slow, but %v is busy", room)
	}
}

// refusingDriver answers every query with one SQLSTATE, as a server with no
// room for another client does.
type refusingDriver struct{ code string }

func (d refusingDriver) Open(string) (driver.Conn, error) { return refusingConn(d), nil }

type refusingConn refusingDriver

func (refusingConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("no prepare") }
func (refusingConn) Close() error                        { return nil }
func (refusingConn) Begin() (driver.Tx, error)           { return nil, errors.New("no transactions") }
func (c refusingConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return nil, &pgconn.PgError{Code: c.code, Message: "sorry, too many clients already"}
}

var refusingDriverSeq atomic.Int32

// A server that refuses a lookup because it has no room says so with a
// SQLSTATE, which is what is read -- never the message text, which a server
// localises -- and the lookup reports it as busy, so the engine waits for room
// instead of failing the message. Any other SQLSTATE is an ordinary failure.
func TestALookupRefusedForLackOfRoomIsBusy(t *testing.T) {
	cases := []struct {
		code string
		busy bool
	}{
		{"53300", true},  // too_many_connections
		{"57P03", true},  // cannot_connect_now
		{"42P01", false}, // undefined_table
		{"57014", false}, // query_canceled
	}
	modes := map[string]map[string]any{
		"table mode": tableLookupConfig(nil),
		"query mode": tableLookupConfig(map[string]any{
			"mode":          "query",
			"queryTemplate": "SELECT status FROM delivery_order_items WHERE id = {{id}}",
		}),
	}
	for _, c := range cases {
		for mode, cfg := range modes {
			t.Run(c.code+"/"+mode, func(t *testing.T) {
				name := fmt.Sprintf("hermod-lookup-refusing-%d", refusingDriverSeq.Add(1))
				sql.Register(name, refusingDriver{code: c.code})
				db, err := sql.Open(name, "")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				reg := &fullFakeRegistry{db: db, source: storage.Source{
					ID: "src1", Name: "orders", Type: "postgres",
					Config: hermod.StringMap{"use_cdc": "false"},
				}}
				ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)

				err = runHungLookup(ctx, t, cfg)
				if err == nil {
					t.Fatal("a refused lookup reported success")
				}
				if got := errors.Is(err, hermod.ErrBusy); got != c.busy {
					t.Errorf("busy = %v, want %v: %v", got, c.busy, err)
				}
			})
		}
	}
}
