// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package txsql

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/gsoultan/hermod"
	"github.com/jackc/pgx/v5/pgconn"
	mssql "github.com/microsoft/go-mssqldb"
)

func TestIsPermanentDBError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"pg unique violation", &pgconn.PgError{Code: "23505"}, true},
		{"pg foreign key", &pgconn.PgError{Code: "23503"}, true},
		{"pg not null", &pgconn.PgError{Code: "23502"}, true},
		{"pg bad cast", &pgconn.PgError{Code: "22P02"}, true},
		{"pg undefined table", &pgconn.PgError{Code: "42P01"}, true},
		{"pg serialization failure", &pgconn.PgError{Code: "40001"}, false},
		{"pg deadlock", &pgconn.PgError{Code: "40P01"}, false},
		{"pg lock timeout", &pgconn.PgError{Code: "55P03"}, false},
		{"pg admin shutdown", &pgconn.PgError{Code: "57P01"}, false},
		{"wrapped pg unique", fmt.Errorf("exec: %w", &pgconn.PgError{Code: "23505"}), true},
		{"mysql duplicate", &mysql.MySQLError{Number: 1062}, true},
		{"mysql foreign key", &mysql.MySQLError{Number: 1452}, true},
		{"mysql not null", &mysql.MySQLError{Number: 1048}, true},
		{"mysql unknown column", &mysql.MySQLError{Number: 1054}, true},
		{"mysql deadlock", &mysql.MySQLError{Number: 1213}, false},
		{"mysql lock wait", &mysql.MySQLError{Number: 1205}, false},
		{"mssql duplicate", mssql.Error{Number: 2627}, true},
		{"mssql unique index", mssql.Error{Number: 2601}, true},
		{"mssql foreign key", mssql.Error{Number: 547}, true},
		{"mssql not null", mssql.Error{Number: 515}, true},
		{"mssql deadlock", mssql.Error{Number: 1205}, false},
		{"bad connection", driver.ErrBadConn, false},
		{"deadline", context.DeadlineExceeded, false},
		{"canceled", context.Canceled, false},
		{"unknown", errors.New("connection reset by peer"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPermanentDBError(tc.err); got != tc.want {
				t.Errorf("isPermanentDBError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestClassifyWrapsOnlyPermanentErrors(t *testing.T) {
	if err := classify(&pgconn.PgError{Code: "23505"}); !errors.Is(err, hermod.ErrPermanent) {
		t.Errorf("unique violation not marked permanent: %v", err)
	}
	if err := classify(&pgconn.PgError{Code: "40P01"}); errors.Is(err, hermod.ErrPermanent) {
		t.Errorf("deadlock marked permanent: %v", err)
	}
	var pg *pgconn.PgError
	if !errors.As(classify(&pgconn.PgError{Code: "23505"}), &pg) {
		t.Error("classify hid the database error from errors.As")
	}
}
