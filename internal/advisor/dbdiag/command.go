// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package dbdiag

import "strings"

// keywords are the words a statement's command may start with. Reading stops
// at the first word that is not one, so no identifier or literal is kept.
var keywords = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true, "MERGE": true, "WITH": true,
	"CREATE": true, "ALTER": true, "DROP": true, "TRUNCATE": true, "INDEX": true, "UNIQUE": true,
	"CONCURRENTLY": true, "TABLE": true, "MATERIALIZED": true, "VIEW": true, "REFRESH": true,
	"VACUUM": true, "ANALYZE": true, "FULL": true, "REINDEX": true, "CLUSTER": true, "COPY": true,
	"LOCK": true, "BEGIN": true, "COMMIT": true, "ROLLBACK": true, "FETCH": true, "DECLARE": true,
	"CALL": true, "DO": true, "EXPLAIN": true,
}

// command is the leading keywords of a statement, at most four, upper-cased:
// "CREATE INDEX CONCURRENTLY", never the table or a value.
func command(query string) string {
	var out []string
	for _, w := range strings.Fields(query) {
		w = strings.ToUpper(strings.TrimRight(w, ";("))
		if w == "" {
			continue
		}
		if !keywords[w] || len(out) == 4 {
			break
		}
		out = append(out, w)
	}
	// "BEGIN; SELECT ..." sent as one string says nothing with BEGIN in front.
	if len(out) > 1 && out[0] == "BEGIN" {
		out = out[1:]
	}
	return strings.Join(out, " ")
}
