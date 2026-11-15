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
	"errors"
	"fmt"

	"github.com/go-sql-driver/mysql"
	"github.com/gsoultan/hermod"
	"github.com/jackc/pgx/v5/pgconn"
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/sijms/go-ora/v2/network"
	"modernc.org/sqlite"
)

// classify marks err permanent when another attempt with the same message
// would get the same answer, and returns it unchanged otherwise. The database
// error stays reachable through errors.As either way.
func classify(err error) error {
	if err == nil || errors.Is(err, hermod.ErrPermanent) || !isPermanentDBError(err) {
		return err
	}
	return permanent(err)
}

func permanent(err error) error {
	return fmt.Errorf("%w: %w", hermod.ErrPermanent, err)
}

// isPermanentDBError reports a database error caused by the data or the
// statement -- a constraint, a bad value, a missing table -- as opposed to
// the moment: a deadlock, a lock timeout, a dropped connection. The first
// fails again on every retry and belongs in the dead-letter sink; the second
// may well succeed next time. Anything unrecognised is treated as transient,
// because retrying a permanent error only delays its dead letter, while
// parking a transient one loses a message that would have gone through.
func isPermanentDBError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		if len(pg.Code) < 2 {
			return false
		}
		switch pg.Code[:2] {
		case "22", // data exception: bad cast, out of range, too long
			"23", // integrity constraint violation
			"42", // syntax error or access rule: undefined table or column
			"44": // WITH CHECK OPTION violation
			return true
		}
		return false
	}

	var my *mysql.MySQLError
	if errors.As(err, &my) {
		switch my.Number {
		case 1048, // column cannot be null
			1054, // unknown column
			1062, // duplicate entry
			1064, // syntax error
			1146, // table doesn't exist
			1264, // out of range
			1292, // incorrect value
			1364, // field has no default
			1366, // incorrect value for column
			1406, // data too long
			1451, // foreign key: row is referenced
			1452, // foreign key: parent row missing
			1690, // value out of range
			3819: // check constraint violated
			return true
		}
		return false
	}

	if n, ok := mssqlNumber(err); ok {
		switch n {
		case 102, // syntax error
			207,  // invalid column name
			208,  // invalid object name
			245,  // conversion failed
			515,  // cannot insert NULL
			547,  // foreign key or check constraint
			2601, // duplicate key in unique index
			2627, // primary key or unique constraint
			2628, // string or binary data would be truncated
			8114, // error converting data type
			8115, // arithmetic overflow
			8152: // string or binary data would be truncated
			return true
		}
		return false
	}

	var ora *network.OracleError
	if errors.As(err, &ora) {
		switch ora.ErrCode {
		case 1, // unique constraint violated
			904,   // invalid identifier
			942,   // table or view does not exist
			1400,  // cannot insert NULL
			1407,  // cannot update to NULL
			1438,  // value larger than the column's precision
			1722,  // invalid number
			1830,  // date format picture ends before converting the whole string
			1840,  // input value not long enough for date format
			1841,  // year out of range
			1843,  // not a valid month
			1847,  // day of month out of range
			1858,  // a non-numeric character where a numeric was expected
			1861,  // literal does not match format string
			2290,  // check constraint violated
			2291,  // integrity constraint violated: parent key not found
			2292,  // integrity constraint violated: child record found
			12899: // value too large for column
			return true
		}
		return false
	}

	var lite *sqlite.Error
	if errors.As(err, &lite) {
		switch lite.Code() & 0xff {
		case 1, // SQLITE_ERROR: no such table or column, syntax
			19, // SQLITE_CONSTRAINT
			20, // SQLITE_MISMATCH
			25: // SQLITE_RANGE
			return true
		}
		return false
	}
	return false
}

func mssqlNumber(err error) (int32, bool) {
	var v mssql.Error
	if errors.As(err, &v) {
		return v.Number, true
	}
	var p *mssql.Error
	if errors.As(err, &p) && p != nil {
		return p.Number, true
	}
	return 0, false
}
