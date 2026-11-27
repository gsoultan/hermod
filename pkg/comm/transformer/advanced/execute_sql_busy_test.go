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
	"database/sql/driver"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/jackc/pgx/v5/pgconn"
)

// refusingDriver refuses every statement with one SQLSTATE, as a server with
// no room for another client does before running anything.
type refusingDriver struct{ code string }

func (d refusingDriver) Open(string) (driver.Conn, error) { return refusingConn(d), nil }

type refusingConn refusingDriver

func (refusingConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("no prepare") }
func (refusingConn) Close() error                        { return nil }
func (refusingConn) Begin() (driver.Tx, error)           { return nil, errors.New("no transactions") }
func (c refusingConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return nil, &pgconn.PgError{Code: c.code, Message: "sorry, too many clients already"}
}
func (c refusingConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return nil, &pgconn.PgError{Code: c.code, Message: "sorry, too many clients already"}
}

var refusingDriverSeq atomic.Int32

// An SQL step whose server refused it for lack of room ran nothing, so it is
// reported as busy and the engine waits for room instead of failing the
// message. Only the SQLSTATEs sent before a statement runs count: repeating a
// write that may have run would apply it twice. Read by code, never by the
// message text, which a server localises.
func TestAnSQLStepRefusedForLackOfRoomIsBusy(t *testing.T) {
	cases := []struct {
		code string
		busy bool
	}{
		{"53300", true},  // too_many_connections
		{"57P03", true},  // cannot_connect_now
		{"23505", false}, // unique_violation
		{"40001", false}, // serialization_failure: the statement ran
	}
	for _, c := range cases {
		for _, resultField := range []string{"", "returned"} {
			t.Run(fmt.Sprintf("%s/result=%q", c.code, resultField), func(t *testing.T) {
				name := fmt.Sprintf("hermod-execsql-refusing-%d", refusingDriverSeq.Add(1))
				sql.Register(name, refusingDriver{code: c.code})
				db, err := sql.Open(name, "")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				reg := &execSQLFakeRegistry{db: db, source: storage.Source{ID: "src1", Type: "postgres"}}

				cfg := map[string]any{"sourceId": "src1", "queryTemplate": "INSERT INTO audit VALUES ('a','b')"}
				if resultField != "" {
					cfg["resultField"] = resultField
				}
				_, err = runExecSQL(t, &ExecuteSQLTransformer{}, reg, cfg, nil)
				if err == nil {
					t.Fatal("a refused statement reported success")
				}
				if got := errors.Is(err, hermod.ErrBusy); got != c.busy {
					t.Errorf("busy = %v, want %v: %v", got, c.busy, err)
				}
			})
		}
	}
}
