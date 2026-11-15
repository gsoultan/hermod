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

// Oracle.
//
// What is different about Oracle, and why the code looks the way it does:
//
//   - A write has no RETURNING result set. It hands columns back through OUT
//     bind variables named in an INTO clause. The sink writes that clause, so a
//     statement reads the same on Oracle as on PostgreSQL: RETURNING id, and
//     later statements read {{name.id}}. Scalar OUT binds hold one row, so a
//     statement that gives columns back must change exactly one row.
//   - The values come back as text. Oracle converts a NUMBER or DATE to text
//     for the OUT bind and back again when a later statement binds it, under
//     the session's NLS settings, which are the same at both ends.
//   - Column names come back in capitals, because Oracle folds an unquoted
//     name to upper case. A row's keys are lower-cased, so {{order.id}} reads
//     the same column on every database.
//   - go-ora opens a transaction only at the default isolation, read
//     committed. Serializable is set by statement once the transaction is
//     open; repeatable read and read uncommitted do not exist on Oracle.
//   - A procedure call or PL/SQL block can commit inside itself, and DDL
//     commits on its own: both are refused, as a preview refuses them.
//   - A trailing semicolon is a syntax error outside PL/SQL, so it is dropped.
//   - Guided mode quotes names as Oracle resolves them unquoted: a name with
//     no capitals is folded to upper case, one with capitals is taken as
//     written. MERGE cannot return columns, so an upsert that gives columns
//     back is compiled to the MERGE and a SELECT of the row by its key.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	go_ora "github.com/sijms/go-ora/v2"
)

// oracleOutSize is the most bytes an OUT bind reads back: VARCHAR2's limit
// without extended strings.
const oracleOutSize = 4000

func oracleIsolation(name string, level sql.IsolationLevel) (sql.IsolationLevel, error) {
	switch level {
	case sql.LevelDefault, sql.LevelReadCommitted:
		return sql.LevelDefault, nil
	case sql.LevelSerializable:
		return sql.LevelSerializable, nil
	}
	return 0, fmt.Errorf("txsql: Oracle has no %s isolation; use read_committed or serializable", name)
}

// prepareOracle adapts one parsed statement to Oracle: the trailing
// semicolon dropped, and a RETURNING clause turned into OUT binds.
func prepareOracle(s *Statement, i int) error {
	s.SQL = strings.TrimRight(strings.TrimSpace(s.SQL), "; \t\r\n")
	pos := lastKeyword(s.SQL, "RETURNING")
	if pos < 0 {
		return nil
	}
	cols, err := returningColumns(s.SQL[pos+len("RETURNING"):])
	if err != nil {
		return fmt.Errorf("txsql: %s: %w", label(s, i), err)
	}
	if s.Expect != ExpectOne {
		return fmt.Errorf("txsql: %s gives columns back, which Oracle does only for a statement that changes exactly one row; "+
			"set expect to one, or add a SELECT after it", label(s, i))
	}
	s.outCols = cols
	s.rows = false
	return nil
}

// lastKeyword is where the last word kw (any case) outside literals,
// comments and tokens starts in text, or -1.
func lastKeyword(text, kw string) int {
	found := -1
	for i := 0; i < len(text); {
		next, _ := skipNonCode(text, i)
		if next < 0 {
			break
		}
		if next > i {
			i = next
			continue
		}
		end := i + len(kw)
		if end <= len(text) && strings.EqualFold(text[i:end], kw) &&
			(i == 0 || !isWordByte(text[i-1])) && (end == len(text) || !isWordByte(text[end])) {
			found = i
		}
		i++
	}
	return found
}

func isWordByte(b byte) bool {
	return b == '_' || b == '$' || b == '#' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// returningColumns reads the columns of a RETURNING clause: plain or
// double-quoted names, nothing else, since each one becomes the key a later
// statement reads it under.
func returningColumns(tail string) ([]string, error) {
	if lastKeyword(tail, "INTO") >= 0 {
		return nil, errors.New("write RETURNING without INTO: the sink adds the INTO clause and its bind variables itself")
	}
	var cols []string
	for _, part := range strings.Split(tail, ",") {
		name := strings.TrimSpace(part)
		if len(name) > 2 && name[0] == '"' && name[len(name)-1] == '"' && !strings.Contains(name[1:len(name)-1], `"`) {
			name = name[1 : len(name)-1]
		} else if !identRe.MatchString(name) {
			return nil, fmt.Errorf("on Oracle, RETURNING lists column names only, not %q, "+
				"because each becomes the name a later statement reads it under", strings.TrimSpace(part))
		}
		cols = append(cols, strings.ToLower(name))
	}
	return cols, nil
}

// withInto appends the INTO clause for a statement already bound with
// nArgs parameters: :nArgs+1 and on, one per column.
func withInto(boundSQL string, nArgs, nOut int) string {
	binds := make([]string, nOut)
	for i := range binds {
		binds[i] = fmt.Sprintf(":%d", nArgs+i+1)
	}
	return boundSQL + " INTO " + strings.Join(binds, ", ")
}

// lowerKeys lower-cases a row's column names; see the package notes above.
func lowerKeys(row map[string]any) map[string]any {
	if row == nil {
		return nil
	}
	out := make(map[string]any, len(row))
	for k, v := range row {
		out[strings.ToLower(k)] = v
	}
	return out
}

// oq quotes a name as Oracle would resolve it unquoted when it has no
// capitals, and as written when it has.
func oq(name string) string {
	if !strings.ContainsFunc(name, unicode.IsUpper) {
		name = strings.ToUpper(name)
	}
	return `"` + name + `"`
}

// compileOracle: MERGE for an upsert, and RETURNING, which the sink turns
// into OUT binds, for the given-back columns of an insert, update or delete.
func compileOracle(action string, s step, st *Statement, _ []string) (string, error) {
	returning := ""
	if len(s.returns) > 0 {
		returning = " RETURNING " + strings.Join(s.returns, ", ")
		// Scalar OUT binds hold one row.
		st.Expect = ExpectOne
	}
	switch action {
	case ActionInsert:
		return insertHead(s) + values(s) + returning, nil
	case ActionUpdate:
		return fmt.Sprintf("UPDATE %s SET %s%s%s", s.table, assignments(s, s.sets), where(s), returning), nil
	case ActionDelete:
		return fmt.Sprintf("DELETE FROM %s%s%s", s.table, where(s), returning), nil
	}
	src := make([]string, len(s.cols))
	srcCols := make([]string, len(s.cols))
	for i, c := range s.cols {
		src[i] = s.vals[i] + " AS " + c
		srcCols[i] = "s." + c
	}
	on := make([]string, 0, len(s.match))
	for _, k := range matchIdx(s) {
		on = append(on, "t."+s.cols[k]+" = s."+s.cols[k])
	}
	sql := fmt.Sprintf("MERGE INTO %s t USING (SELECT %s FROM dual) s ON (%s)",
		s.table, strings.Join(src, ", "), strings.Join(on, " AND "))
	// Oracle cannot update a column the ON clause reads, and allows a MERGE
	// with only the insert branch.
	if len(s.sets) > 0 {
		upd := make([]string, len(s.sets))
		for i, k := range s.sets {
			upd[i] = "t." + s.cols[k] + " = s." + s.cols[k]
		}
		sql += " WHEN MATCHED THEN UPDATE SET " + strings.Join(upd, ", ")
	}
	return sql + fmt.Sprintf(" WHEN NOT MATCHED THEN INSERT (%s) VALUES (%s)",
		strings.Join(s.cols, ", "), strings.Join(srcCols, ", ")), nil
}

// oracleReadBack compiles an upsert that gives columns back: the MERGE, then
// a SELECT of the columns by the match key, under the step's name.
func oracleReadBack(d sqlDialect, s step, g *GuidedStep) ([]Statement, error) {
	upsert := s
	upsert.returns = nil
	mergeName := ""
	if name := strings.TrimSpace(g.Name); name != "" {
		mergeName = name + "_merge"
	}
	merge, err := compileOne(d, upsert, &GuidedStep{Name: mergeName, Action: ActionUpsert, ForEach: g.ForEach, As: g.As})
	if err != nil {
		return nil, err
	}
	sel := Statement{
		Name:    g.Name,
		ForEach: g.ForEach,
		As:      g.As,
		SQL:     fmt.Sprintf("SELECT %s FROM %s%s", strings.Join(s.returns, ", "), s.table, where(s)),
		Expect:  ExpectOne,
	}
	return []Statement{merge, sel}, nil
}

// execOut runs an Oracle write whose RETURNING columns come back through OUT
// binds, and returns them as the row later statements read.
func execOut(ctx context.Context, q sqlutil.Querier, st *Statement, b sqlutil.TemplateBinding, step *stepRecord) (map[string]any, error) {
	outs := make([]sql.NullString, len(st.outCols))
	args := make([]any, 0, len(b.Args)+len(outs))
	args = append(args, b.Args...)
	for i := range outs {
		args = append(args, go_ora.Out{Dest: &outs[i], Size: oracleOutSize})
	}
	res, err := q.ExecContext(ctx, withInto(b.SQL, len(b.Args), len(outs)), args...)
	if err != nil {
		return nil, classify(err)
	}
	n, err := res.RowsAffected()
	step.changed(n, err)
	if err != nil {
		return nil, fmt.Errorf("the driver cannot count changed rows, so expect %q cannot be checked: %w", st.Expect, err)
	}
	if err := check(st.Expect, n, "changed"); err != nil {
		return nil, err
	}
	row := make(map[string]any, len(outs))
	for i, c := range st.outCols {
		if outs[i].Valid {
			row[c] = outs[i].String
		} else {
			row[c] = nil
		}
	}
	step.returned([]map[string]any{row}, 1)
	return row, nil
}
