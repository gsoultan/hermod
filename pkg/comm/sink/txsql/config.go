// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package txsql is the Transactional SQL sink: it runs an ordered list of
// statements against one database inside a single transaction per batch, and
// lets a later statement use what an earlier one returned.
//
// It exists for the write that one table cannot hold: an order and its lines,
// an invoice and its rows, an account and its audit entry. The parent's
// generated key is only known once it is inserted, so the child statement has
// to run after it and read that key back -- and if the child fails, the parent
// must not be left behind. The Execute SQL step can already pass a RETURNING
// row on, but outside a preview each of its statements commits on its own; a
// child that fails there leaves an orphaned parent.
//
// A batch is one transaction. Any data error rolls the whole batch back and is
// reported as hermod.ErrPermanent, which makes the engine write each message on
// its own and park only the one that cannot be written. A transient error rolls
// back too and is returned as it is, so the engine retries the batch.
package txsql

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

// MaxStatements bounds one sink's statement list. Each statement is a round
// trip inside a transaction that holds its locks until commit; past a few
// dozen, the workflow wants restructuring, not a longer list.
const MaxStatements = 50

// MaxForEachItems bounds how many times one statement runs for one message.
// The count comes from message data that nothing upstream bounds.
const MaxForEachItems = 10000

// DefaultStatementTimeout bounds one statement, so a lock held elsewhere
// fails the batch instead of holding the writer forever.
const DefaultStatementTimeout = 30 * time.Second

// What to do with a template variable that resolved to nothing.
const (
	// OnUnresolvedFail refuses the message. The default: in a dependent write
	// a NULL foreign key is exactly the failure this sink exists to prevent,
	// and a typo in a path looks the same as an absent field.
	OnUnresolvedFail = "fail"
	// OnUnresolvedNull binds NULL, as the Execute SQL step does by default.
	OnUnresolvedNull = "null"
)

// What a statement must have done for the message to commit.
const (
	// ExpectOne: exactly one row returned, or for a statement that returns no
	// rows, exactly one row changed.
	ExpectOne = "one"
	// ExpectAffected: at least one row returned or changed.
	ExpectAffected = "affected"
	// ExpectAny: no check.
	ExpectAny = "any"
)

// drivers are the database types this sink runs on, by the name a sink
// config uses. Oracle waits for RETURNING ... INTO support; ClickHouse and
// Snowflake have no transaction this could use.
var drivers = map[string]bool{
	"postgres": true, "yugabyte": true,
	"mysql": true, "mariadb": true,
	"mssql": true, "sqlserver": true,
	"sqlite": true,
}

var isolationLevels = map[string]sql.IsolationLevel{
	"":                 sql.LevelDefault,
	"default":          sql.LevelDefault,
	"read_committed":   sql.LevelReadCommitted,
	"repeatable_read":  sql.LevelRepeatableRead,
	"serializable":     sql.LevelSerializable,
	"read_uncommitted": sql.LevelReadUncommitted,
}

var nameRe = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// Statement is one step of the transaction.
type Statement struct {
	// Name is how later statements refer to what this one returned:
	// {{name.column}}. Optional; an unnamed statement is called stepN.
	Name string `json:"name,omitempty"`
	// SQL is one statement. {{path}} tokens are bound as parameters, never
	// spliced into the text.
	SQL string `json:"sql"`
	// ForEach is a message path to a list. The statement runs once per
	// element, which it reads as {{As}} or {{As.field}}.
	ForEach string `json:"forEach,omitempty"`
	// As names the current element of ForEach. Defaults to "item".
	As string `json:"as,omitempty"`
	// Expect is checked after every run: ExpectOne, ExpectAffected or
	// ExpectAny. Defaults to ExpectOne for a statement that returns rows and
	// ExpectAny otherwise.
	Expect string `json:"expect,omitempty"`
	// ReturnID binds MySQL's last-insert id as {{name.id}} after an INSERT
	// that returns no rows. Defaults to true; MySQL has no RETURNING.
	ReturnID *bool `json:"returnId,omitempty"`

	// rows is whether the statement hands rows back, decided once from the
	// text.
	rows bool
}

// Config is a parsed, validated sink configuration.
type Config struct {
	// Driver is the database type: postgres, yugabyte, mysql, mariadb, mssql
	// or sqlite.
	Driver           string
	Isolation        sql.IsolationLevel
	OnUnresolved     string
	StatementTimeout time.Duration
	Statements       []Statement
}

// ParseConfig reads and validates a stored sink configuration.
//
// Everything that can be refused without a database is refused here, so the
// sink form, the test button and workflow start all report the same problem
// in the same words, instead of the first message failing on it.
func ParseConfig(m map[string]string) (Config, error) {
	cfg := Config{
		Driver:           strings.ToLower(strings.TrimSpace(m["driver"])),
		OnUnresolved:     strings.ToLower(strings.TrimSpace(m["on_unresolved"])),
		StatementTimeout: DefaultStatementTimeout,
	}
	if cfg.Driver == "" {
		return Config{}, errors.New("txsql: a driver is required (postgres, yugabyte, mysql, mariadb, mssql or sqlite)")
	}
	if !drivers[cfg.Driver] {
		return Config{}, fmt.Errorf("txsql: %q is not supported; use postgres, yugabyte, mysql, mariadb, mssql or sqlite", cfg.Driver)
	}

	switch cfg.OnUnresolved {
	case "":
		cfg.OnUnresolved = OnUnresolvedFail
	case OnUnresolvedFail, OnUnresolvedNull:
	default:
		return Config{}, fmt.Errorf("txsql: on_unresolved must be %q or %q, not %q", OnUnresolvedFail, OnUnresolvedNull, cfg.OnUnresolved)
	}

	iso := strings.ToLower(strings.TrimSpace(m["isolation"]))
	level, ok := isolationLevels[iso]
	if !ok {
		return Config{}, fmt.Errorf("txsql: isolation %q is not one of read_committed, repeatable_read, serializable", iso)
	}
	cfg.Isolation = level

	if s := strings.TrimSpace(m["statement_timeout"]); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("txsql: statement_timeout %q is not a positive duration such as 30s", s)
		}
		cfg.StatementTimeout = d
	}

	stmts, err := parseStatements(m["statements"])
	if err != nil {
		return Config{}, err
	}
	if err := validateStatements(stmts); err != nil {
		return Config{}, err
	}
	cfg.Statements = stmts
	return cfg, nil
}

func parseStatements(text string) ([]Statement, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("txsql: at least one statement is required")
	}
	var stmts []Statement
	if err := json.Unmarshal([]byte(text), &stmts); err != nil {
		return nil, fmt.Errorf("txsql: statements must be a JSON list of {name, sql, forEach, as, expect}: %w", err)
	}
	if len(stmts) == 0 {
		return nil, errors.New("txsql: at least one statement is required")
	}
	if len(stmts) > MaxStatements {
		return nil, fmt.Errorf("txsql: %d statements; at most %d run in one transaction", len(stmts), MaxStatements)
	}
	return stmts, nil
}

// validateStatements fills defaults and refuses a list that cannot run.
func validateStatements(stmts []Statement) error {
	index := make(map[string]int, len(stmts))
	for i := range stmts {
		if err := normalize(&stmts[i], i, index); err != nil {
			return err
		}
		index[stmts[i].Name] = i
	}
	// References are checked once every name is known, so a reference to a
	// later statement can be told from a message field.
	for i := range stmts {
		if err := checkReferences(stmts, i, index); err != nil {
			return err
		}
	}
	return nil
}

func label(s *Statement, i int) string {
	return fmt.Sprintf("statement %d (%s)", i+1, s.Name)
}

// normalize fills one statement's defaults and checks what it can on its own.
func normalize(s *Statement, i int, seen map[string]int) error {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		s.Name = fmt.Sprintf("step%d", i+1)
	}
	if !nameRe.MatchString(s.Name) {
		return fmt.Errorf("txsql: %s: the name %q may use only lower-case letters, digits and _, and cannot start with a digit", label(s, i), s.Name)
	}
	if _, dup := seen[s.Name]; dup {
		return fmt.Errorf("txsql: the name %q is used more than once", s.Name)
	}
	if strings.TrimSpace(s.SQL) == "" {
		return fmt.Errorf("txsql: %s has no SQL", label(s, i))
	}
	code := stripLiterals(s.SQL)
	if kw := firstKeyword(code); ddl[kw] {
		return fmt.Errorf("txsql: %s is %s; this sink writes rows, and schema changes do not belong in a per-message transaction", label(s, i), kw)
	}
	if hasInnerSemicolon(code) {
		return fmt.Errorf("txsql: %s holds more than one statement; give each its own entry so its result can be named and checked (one statement per entry)", label(s, i))
	}
	s.rows = returnsRowsCode(code)

	s.ForEach = strings.TrimSpace(s.ForEach)
	s.As = strings.TrimSpace(s.As)
	if s.ForEach != "" && s.As == "" {
		s.As = "item"
	}
	if s.As != "" && !nameRe.MatchString(s.As) {
		return fmt.Errorf("txsql: %s: the item alias %q may use only lower-case letters, digits and _", label(s, i), s.As)
	}
	return normalizeExpect(s, i)
}

func normalizeExpect(s *Statement, i int) error {
	s.Expect = strings.ToLower(strings.TrimSpace(s.Expect))
	switch s.Expect {
	case "":
		s.Expect = ExpectAny
		if s.rows {
			s.Expect = ExpectOne
		}
	case ExpectOne, ExpectAffected, ExpectAny:
	default:
		return fmt.Errorf("txsql: %s: expect must be one, affected or any, not %q", label(s, i), s.Expect)
	}
	return nil
}

// checkReferences refuses a {{name...}} token that names this statement, a
// later one, or one that runs once per item.
func checkReferences(stmts []Statement, i int, index map[string]int) error {
	s := &stmts[i]
	if s.As != "" {
		if _, clash := index[s.As]; clash {
			return fmt.Errorf("txsql: %s: the item alias %q is also a statement name; pick another alias", label(s, i), s.As)
		}
	}
	for _, head := range tokenHeads(s.SQL) {
		j, named := index[head]
		if !named || head == s.As {
			continue
		}
		switch {
		case j == i:
			return fmt.Errorf("txsql: %s uses {{%s...}}, its own result; a statement can only use what ran before it", label(s, i), head)
		case j > i:
			return fmt.Errorf("txsql: %s uses {{%s...}}, which runs later; move it below %q", label(s, i), head, head)
		case stmts[j].ForEach != "":
			return fmt.Errorf("txsql: %s uses {{%s...}}, but %q runs once per item, so it has no single result to use", label(s, i), head, head)
		}
	}
	return nil
}

var ddl = map[string]bool{
	"CREATE": true, "ALTER": true, "DROP": true, "TRUNCATE": true,
	"GRANT": true, "REVOKE": true, "RENAME": true, "COMMENT": true,
}

var tokenRe = regexp.MustCompile(`\{\{\s*([^{}]*?)\s*\}\}`)

// tokenHeads lists the first path segment of every {{token}} in text.
func tokenHeads(text string) []string {
	var heads []string
	for _, m := range tokenRe.FindAllStringSubmatch(text, -1) {
		p := strings.TrimPrefix(strings.TrimSpace(m[1]), ".")
		if p == "" || strings.ContainsAny(p[:1], `'"`) {
			continue // a quoted literal, not a path
		}
		head, _, _ := strings.Cut(p, ".")
		heads = append(heads, head)
	}
	return heads
}

// returnsRows reports whether a statement hands rows back and so has to run
// as a query: a SELECT, a CTE ending in one, or a write with RETURNING or
// OUTPUT. Decided from the text with literals and comments removed, so a
// value that happens to spell "returning" changes nothing.
func returnsRows(sqlText string) bool {
	return returnsRowsCode(stripLiterals(sqlText))
}

var rowsClauseRe = regexp.MustCompile(`\b(RETURNING|OUTPUT)\b`)
var dmlRe = regexp.MustCompile(`\b(INSERT|UPDATE|DELETE|MERGE)\b`)

func returnsRowsCode(code string) bool {
	upper := strings.ToUpper(code)
	if rowsClauseRe.MatchString(upper) {
		return true
	}
	switch firstKeyword(code) {
	case "SELECT", "VALUES":
		return true
	case "WITH":
		return !dmlRe.MatchString(upper)
	}
	return false
}

func firstKeyword(code string) string {
	code = strings.TrimLeft(code, " \t\r\n(")
	end := strings.IndexFunc(code, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && r != '_'
	})
	if end < 0 {
		end = len(code)
	}
	return strings.ToUpper(code[:end])
}

// hasInnerSemicolon reports a ';' that is followed by anything but space.
func hasInnerSemicolon(code string) bool {
	i := strings.IndexByte(code, ';')
	return i >= 0 && strings.TrimSpace(strings.ReplaceAll(code[i:], ";", "")) != ""
}

// stripLiterals blanks out string literals, quoted identifiers, comments and
// {{tokens}}, keeping everything else, so keyword checks see only SQL.
func stripLiterals(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		next, blank := skipNonCode(s, i)
		if next < 0 {
			break // unterminated: nothing after it is SQL
		}
		if next == i {
			b.WriteByte(s[i])
			i++
			continue
		}
		b.WriteString(blank)
		i = next
	}
	return b.String()
}

// skipNonCode returns where the token, comment or quoted text starting at i
// ends and what to write in its place; i itself when s[i] starts none of
// them, and -1 when it starts one that never ends.
func skipNonCode(s string, i int) (int, string) {
	rest := s[i:]
	switch {
	case strings.HasPrefix(rest, "{{"):
		return endAfter(s, i, "}}"), " ? "
	case strings.HasPrefix(rest, "--"):
		if end := strings.IndexByte(rest, '\n'); end >= 0 {
			return i + end, " "
		}
		return -1, ""
	case strings.HasPrefix(rest, "/*"):
		return endAfter(s, i, "*/"), " "
	case s[i] == '\'' || s[i] == '"' || s[i] == '`':
		return skipQuoted(s, i), " x "
	}
	return i, ""
}

func endAfter(s string, i int, closer string) int {
	end := strings.Index(s[i+2:], closer)
	if end < 0 {
		return -1
	}
	return i + 2 + end + len(closer)
}

// skipQuoted returns the index after the quoted text starting at i, where a
// doubled quote is an escaped one.
func skipQuoted(s string, i int) int {
	q := s[i]
	for j := i + 1; j < len(s); j++ {
		if s[j] != q {
			continue
		}
		if j+1 < len(s) && s[j+1] == q {
			j++
			continue
		}
		return j + 1
	}
	return -1
}

// canonicalDriver is the database/sql driver name for cfg.Driver.
func (c Config) canonicalDriver() string {
	d, _ := sqlutil.CanonicalDriver(c.Driver)
	return d
}
