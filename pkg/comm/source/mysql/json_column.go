// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package mysql

import (
	"github.com/go-mysql-org/go-mysql/schema"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

// binlogRowToData turns one binlog row image into the row map the pipeline
// works with.
//
// This used to stringify every driver value, which left a JSON column as an
// opaque string holding JSON. The query paths in this source did the same, so
// the two agreed and nothing in the product contradicted itself loudly enough
// to be noticed -- unlike PostgreSQL, where pgx decoded jsonb on one path and
// the hand-written CDC decoder did not, and the workflow editor ended up
// offering nested field paths that the running pipeline could not resolve.
//
// The column's declared type decides, never its content: a VARCHAR holding
// `{"a":1}` stays a string. go-mysql has already turned MySQL's binary JSON
// into text by the time OnRow sees it, and it hands that text over as a Go
// string rather than as []byte -- sqlutil.DecodeValue accepts both, and the
// unit tests here missed the string case entirely until a live server showed it.
func binlogRowToData(cols []schema.TableColumn, row []any) map[string]any {
	data := make(map[string]any, len(cols))
	for i, col := range cols {
		if i >= len(row) {
			// The binlog and the cached table schema can disagree after a DDL.
			break
		}
		data[col.Name] = sqlutil.DecodeValue(row[i], col.Type == schema.TYPE_JSON)
	}
	return data
}
