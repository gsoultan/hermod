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
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Modes a sink's statements are written in.
const (
	// ModeSQL: the user writes the statements.
	ModeSQL = "sql"
	// ModeGuided: the user picks tables and maps fields, and the statements
	// are compiled from that for the sink's database.
	ModeGuided = "guided"
)

// Guided-mode actions.
const (
	ActionInsert = "insert"
	ActionUpsert = "upsert"
	ActionUpdate = "update"
	ActionDelete = "delete"
)

// GuidedStep is one table write described without SQL.
type GuidedStep struct {
	Name  string `json:"name,omitempty"`
	Table string `json:"table"`
	// Action is insert, upsert (the default), update or delete.
	Action string `json:"action,omitempty"`
	// Match are the key columns an upsert, update or delete finds its row
	// by. Each must also be in Columns, which gives its value.
	Match   []string      `json:"match,omitempty"`
	Columns []ColumnValue `json:"columns"`
	ForEach string        `json:"forEach,omitempty"`
	As      string        `json:"as,omitempty"`
	// Returning are the columns later steps may read as {{name.column}}.
	Returning []string `json:"returning,omitempty"`
}

// ColumnValue sets one column from one template field.
type ColumnValue struct {
	Column string `json:"column"`
	// Value is a single {{path}}: a message field, an item field or an
	// earlier step's column.
	Value string `json:"value"`
}

var (
	identRe       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)
	singleTokenRe = regexp.MustCompile(`^\{\{\s*[^{}]+?\s*\}\}$`)
)

// CompileGuided turns a Guided step list into statements for driver. The
// statements still go through the same checks SQL mode's do.
//
// Identifiers are checked to be plain names and quoted for the database, and
// every value is a single {{field}}, bound as a parameter like any other: no
// part of a step reaches the SQL text except names that cannot carry SQL.
func CompileGuided(driver, stepsJSON string) ([]Statement, error) {
	canonical := Config{Driver: strings.ToLower(strings.TrimSpace(driver))}.canonicalDriver()
	d, ok := guidedDialects[canonical]
	if !ok {
		return nil, fmt.Errorf("txsql: Guided mode does not support %q", driver)
	}
	var steps []GuidedStep
	if strings.TrimSpace(stepsJSON) == "" {
		return nil, errors.New("txsql: Guided mode needs at least one step")
	}
	if err := json.Unmarshal([]byte(stepsJSON), &steps); err != nil {
		return nil, fmt.Errorf("txsql: steps must be a JSON list of {name, table, action, match, columns, forEach, as, returning}: %w", err)
	}
	if len(steps) == 0 {
		return nil, errors.New("txsql: Guided mode needs at least one step")
	}
	if len(steps) > MaxStatements {
		return nil, fmt.Errorf("txsql: %d steps; at most %d run in one transaction", len(steps), MaxStatements)
	}
	out := make([]Statement, len(steps))
	for i := range steps {
		st, err := compileStep(d, &steps[i])
		if err != nil {
			return nil, fmt.Errorf("txsql: step %d: %w", i+1, err)
		}
		out[i] = st
	}
	return out, nil
}

// step is a Guided step whose names and values have been checked and quoted.
type step struct {
	table   string
	cols    []string // quoted, in the order given
	vals    []string // {{tokens}}, matching cols
	match   map[int]bool
	sets    []int // indexes of the columns that are not match columns
	returns []string
}

func compileStep(d sqlDialect, g *GuidedStep) (Statement, error) {
	s, err := checkStep(d, g)
	if err != nil {
		return Statement{}, err
	}
	st := Statement{Name: g.Name, ForEach: g.ForEach, As: g.As}

	action := strings.ToLower(strings.TrimSpace(g.Action))
	if action == "" {
		action = ActionUpsert
	}
	switch action {
	case ActionInsert, ActionUpsert:
		if action == ActionUpsert && len(s.match) == 0 {
			return Statement{}, errors.New("an upsert needs at least one column to match on (a primary key or unique key)")
		}
	case ActionUpdate:
		if len(s.match) == 0 {
			return Statement{}, errors.New("an update needs at least one column to match on, or it would change every row")
		}
		if len(s.sets) == 0 {
			return Statement{}, errors.New("an update needs at least one column to set besides the ones it matches on")
		}
		st.Expect = ExpectAffected
	case ActionDelete:
		if len(s.match) == 0 {
			return Statement{}, errors.New("a delete needs at least one column to match on, or it would delete every row")
		}
		st.Expect = ExpectAny
	default:
		return Statement{}, fmt.Errorf("action %q is not one of insert, upsert, update or delete", g.Action)
	}

	sql, err := d.compile(action, s, &st, g.Returning)
	if err != nil {
		return Statement{}, err
	}
	st.SQL = sql
	return st, nil
}

func checkStep(d sqlDialect, g *GuidedStep) (step, error) {
	table, err := quoteName(d, g.Table, true)
	if err != nil {
		return step{}, fmt.Errorf("table: %w", err)
	}
	s := step{table: table, match: map[int]bool{}}
	pos := map[string]int{}
	for _, c := range g.Columns {
		name := strings.TrimSpace(c.Column)
		q, err := quoteName(d, name, false)
		if err != nil {
			return step{}, fmt.Errorf("column: %w", err)
		}
		if _, dup := pos[strings.ToLower(name)]; dup {
			return step{}, fmt.Errorf("the column %q is set more than once", name)
		}
		v := strings.TrimSpace(c.Value)
		if !singleTokenRe.MatchString(v) {
			return step{}, fmt.Errorf("the value for %q must be one field such as {{customer}}, not %q", name, c.Value)
		}
		pos[strings.ToLower(name)] = len(s.cols)
		s.cols = append(s.cols, q)
		s.vals = append(s.vals, v)
	}
	for _, m := range g.Match {
		i, ok := pos[strings.ToLower(strings.TrimSpace(m))]
		if !ok {
			return step{}, fmt.Errorf("the match column %q is not given a value under columns", m)
		}
		s.match[i] = true
	}
	for i := range s.cols {
		if !s.match[i] {
			s.sets = append(s.sets, i)
		}
	}
	for _, r := range g.Returning {
		q, err := quoteName(d, strings.TrimSpace(r), false)
		if err != nil {
			return step{}, fmt.Errorf("give back: %w", err)
		}
		s.returns = append(s.returns, q)
	}
	if len(s.cols) == 0 && strings.ToLower(g.Action) != ActionDelete {
		return step{}, errors.New("set at least one column")
	}
	return s, nil
}

// quoteName checks name is a plain identifier, or schema.table when
// qualified is allowed, and quotes it for d.
func quoteName(d sqlDialect, name string, qualified bool) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("a name is required")
	}
	parts := strings.Split(name, ".")
	if len(parts) > 2 || (!qualified && len(parts) > 1) {
		return "", fmt.Errorf("%q is not a plain name", name)
	}
	for i, p := range parts {
		if !identRe.MatchString(p) {
			return "", fmt.Errorf("%q may use only letters, digits, _ and $, and cannot start with a digit", name)
		}
		parts[i] = d.quote(p)
	}
	return strings.Join(parts, "."), nil
}

// sqlDialect writes the SQL for one database family.
type sqlDialect struct {
	quote   func(string) string
	compile func(action string, s step, st *Statement, returning []string) (string, error)
}

var guidedDialects = map[string]sqlDialect{
	"pgx":       {quote: dq, compile: compileStandard},
	"sqlite":    {quote: dq, compile: compileStandard},
	"mysql":     {quote: bq, compile: compileMySQL},
	"sqlserver": {quote: sq, compile: compileSQLServer},
}

func dq(s string) string { return `"` + s + `"` }
func bq(s string) string { return "`" + s + "`" }
func sq(s string) string { return "[" + s + "]" }

func pick(list []string, idx []int) []string {
	out := make([]string, len(idx))
	for i, k := range idx {
		out[i] = list[k]
	}
	return out
}

func matchIdx(s step) []int {
	var out []int
	for i := range s.cols {
		if s.match[i] {
			out = append(out, i)
		}
	}
	return out
}

func assignments(s step, idx []int) string {
	parts := make([]string, len(idx))
	for i, k := range idx {
		parts[i] = s.cols[k] + " = " + s.vals[k]
	}
	return strings.Join(parts, ", ")
}

func where(s step) string {
	parts := make([]string, 0, len(s.match))
	for _, k := range matchIdx(s) {
		parts = append(parts, s.cols[k]+" = "+s.vals[k])
	}
	return " WHERE " + strings.Join(parts, " AND ")
}

func insertHead(s step) string {
	return fmt.Sprintf("INSERT INTO %s (%s)", s.table, strings.Join(s.cols, ", "))
}

func values(s step) string {
	return " VALUES (" + strings.Join(s.vals, ", ") + ")"
}

// compileStandard is PostgreSQL, YugabyteDB and SQLite: ON CONFLICT and
// RETURNING. It never writes DO NOTHING, which returns no row on a conflict.
func compileStandard(action string, s step, _ *Statement, _ []string) (string, error) {
	var b strings.Builder
	switch action {
	case ActionInsert, ActionUpsert:
		b.WriteString(insertHead(s) + values(s))
		if action == ActionUpsert {
			sets := s.sets
			if len(sets) == 0 {
				sets = matchIdx(s)[:1]
			}
			parts := make([]string, len(sets))
			for i, k := range sets {
				parts[i] = s.cols[k] + " = EXCLUDED." + s.cols[k]
			}
			fmt.Fprintf(&b, " ON CONFLICT (%s) DO UPDATE SET %s", strings.Join(pick(s.cols, matchIdx(s)), ", "), strings.Join(parts, ", "))
		}
	case ActionUpdate:
		fmt.Fprintf(&b, "UPDATE %s SET %s%s", s.table, assignments(s, s.sets), where(s))
	case ActionDelete:
		fmt.Fprintf(&b, "DELETE FROM %s%s", s.table, where(s))
	}
	if len(s.returns) > 0 {
		b.WriteString(" RETURNING " + strings.Join(s.returns, ", "))
	}
	return b.String(), nil
}

// compileMySQL is MySQL and MariaDB: ON DUPLICATE KEY UPDATE, and the
// generated key read back from the driver's last-insert id.
func compileMySQL(action string, s step, st *Statement, returning []string) (string, error) {
	if len(returning) > 1 {
		return "", errors.New("MySQL can give back only the generated id of an insert; list one column")
	}
	if len(returning) == 1 {
		if action == ActionUpdate || action == ActionDelete {
			return "", errors.New("MySQL cannot give back columns from an update or delete; " +
				"switch to SQL mode and add a SELECT after it")
		}
		st.ReturnIDAs = strings.TrimSpace(returning[0])
	}
	switch action {
	case ActionUpdate:
		return fmt.Sprintf("UPDATE %s SET %s%s", s.table, assignments(s, s.sets), where(s)), nil
	case ActionDelete:
		return fmt.Sprintf("DELETE FROM %s%s", s.table, where(s)), nil
	}
	sql := insertHead(s) + values(s)
	if action == ActionInsert {
		return sql, nil
	}
	parts := make([]string, 0, len(s.sets)+1)
	for _, k := range s.sets {
		parts = append(parts, s.cols[k]+" = VALUES("+s.cols[k]+")")
	}
	if len(s.returns) == 1 {
		// On an update MySQL reports no insert id; this makes it report the
		// existing row's, so a redelivered parent still hands its id on.
		parts = append(parts, s.returns[0]+" = LAST_INSERT_ID("+s.returns[0]+")")
	}
	if len(parts) == 0 {
		k := matchIdx(s)[0]
		parts = append(parts, s.cols[k]+" = "+s.cols[k])
	}
	return sql + " ON DUPLICATE KEY UPDATE " + strings.Join(parts, ", "), nil
}

// compileSQLServer: OUTPUT for the given-back columns and MERGE for an
// upsert, held under HOLDLOCK so two writers cannot both insert the row.
func compileSQLServer(action string, s step, _ *Statement, _ []string) (string, error) {
	output := func(prefix string) string {
		if len(s.returns) == 0 {
			return ""
		}
		parts := make([]string, len(s.returns))
		for i, r := range s.returns {
			parts[i] = prefix + "." + r
		}
		return " OUTPUT " + strings.Join(parts, ", ")
	}
	switch action {
	case ActionInsert:
		return insertHead(s) + output("INSERTED") + values(s), nil
	case ActionUpdate:
		return fmt.Sprintf("UPDATE %s SET %s%s%s", s.table, assignments(s, s.sets), output("INSERTED"), where(s)), nil
	case ActionDelete:
		return fmt.Sprintf("DELETE FROM %s%s%s", s.table, output("DELETED"), where(s)), nil
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
	sets := s.sets
	if len(sets) == 0 {
		sets = matchIdx(s)[:1]
	}
	upd := make([]string, len(sets))
	for i, k := range sets {
		upd[i] = "t." + s.cols[k] + " = s." + s.cols[k]
	}
	return fmt.Sprintf("MERGE INTO %s WITH (HOLDLOCK) AS t USING (SELECT %s) AS s ON %s"+
		" WHEN MATCHED THEN UPDATE SET %s WHEN NOT MATCHED THEN INSERT (%s) VALUES (%s)%s;",
		s.table, strings.Join(src, ", "), strings.Join(on, " AND "),
		strings.Join(upd, ", "), strings.Join(s.cols, ", "), strings.Join(srcCols, ", "), output("INSERTED")), nil
}
