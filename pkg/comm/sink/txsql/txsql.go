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
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/infra/pgxutil"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	"github.com/jackc/pgx/v5/stdlib"
)

// Sink runs the configured statements for every message, one transaction
// per batch.
type Sink struct {
	db  *sql.DB
	cfg Config
	// driver is the database/sql driver name, which decides placeholder style.
	driver string
	// pooled is whether a PostgreSQL connection goes through a transaction
	// pooler, where PREPARE TRANSACTION cannot be used.
	pooled bool

	// open is the transaction Begin opened, which writes join until it ends;
	// see twophase.go.
	mu   sync.Mutex
	open *openTx
}

var (
	_ hermod.Sink      = (*Sink)(nil)
	_ hermod.BatchSink = (*Sink)(nil)
)

// Open builds a sink over a new connection pool to connString. The sink owns
// the pool and closes it in Close.
func Open(cfg Config, connString string) (*Sink, error) {
	db, pooled, err := openDB(cfg, connString)
	if err != nil {
		return nil, err
	}
	s := New(db, cfg)
	s.pooled = pooled
	return s, nil
}

// New builds a sink over db, which it then owns.
func New(db *sql.DB, cfg Config) *Sink {
	return &Sink{db: db, cfg: cfg, driver: cfg.canonicalDriver()}
}

func openDB(cfg Config, connString string) (*sql.DB, bool, error) {
	if strings.TrimSpace(connString) == "" {
		return nil, false, errors.New("txsql: no connection details: set a connection string, or host, port, user and database")
	}
	switch driver := cfg.canonicalDriver(); driver {
	case "pgx":
		pc, pooled, err := pgxutil.ParseConfig(connString)
		if err != nil {
			return nil, false, fmt.Errorf("txsql: parse postgres connection: %w", err)
		}
		return stdlib.OpenDB(*pc), pooled, nil
	case "sqlite":
		dsn := connString
		if !strings.Contains(dsn, "?") {
			// The same defaults the SQLite sink opens with. foreign_keys is
			// the one that matters here: without it SQLite accepts a child
			// row whose parent does not exist.
			dsn += "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
		}
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, false, fmt.Errorf("txsql: open sqlite: %w", err)
		}
		// One writer: SQLite serialises writes anyway, and a second
		// connection would wait on the first's transaction.
		db.SetMaxOpenConns(1)
		return db, false, nil
	default:
		db, err := sql.Open(driver, connString)
		if err != nil {
			return nil, false, fmt.Errorf("txsql: open %s: %w", cfg.Driver, err)
		}
		return db, false, nil
	}
}

// Ping checks the database is reachable.
func (s *Sink) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Close ends a transaction Begin left open, without committing it, and
// closes the connection pool.
func (s *Sink) Close() error {
	if o := s.take(); o != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.abandon(ctx, o)
	}
	return s.db.Close()
}

// Write runs every statement for msg in one transaction.
func (s *Sink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

// WriteBatch runs every statement for every message in one transaction, and
// commits only if all of them succeed.
//
// On a data error the whole batch is rolled back and the error wraps
// hermod.ErrPermanent. The engine then writes each message of the batch on its
// own, so only the message that cannot be written reaches the dead-letter
// sink. Finding that message here instead, with a savepoint per message,
// would save little and add a second partial-failure path to get right.
//
// Between Begin and the end of that transaction the messages run inside it
// instead, and nothing is committed here: a transactional group decides for
// every member together.
func (s *Sink) WriteBatch(ctx context.Context, msgs []hermod.Message) (err error) {
	if len(msgs) == 0 {
		return nil
	}
	if q := s.openQuerier(); q != nil {
		return s.runAll(ctx, q, msgs)
	}
	tx, err := s.beginTx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if err := s.runAll(ctx, tx, msgs); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		// A deferred constraint is checked here, which makes it a data error
		// like any other; a dropped connection leaves the outcome unknown and
		// is retried, which is why the statements should be idempotent.
		return fmt.Errorf("txsql: commit: %w", classify(err))
	}
	return nil
}

func (s *Sink) runAll(ctx context.Context, q sqlutil.Querier, msgs []hermod.Message) error {
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		if err := s.runMessage(ctx, q, msg, nil); err != nil {
			return fmt.Errorf("txsql: message %s: %w", msg.ID(), err)
		}
	}
	return nil
}

// beginTx opens a transaction at the configured isolation.
func (s *Sink) beginTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, s.cfg.txOptions())
	if err != nil {
		return nil, fmt.Errorf("txsql: begin transaction: %w", classify(err))
	}
	if s.driver == "oracle" && s.cfg.Isolation == sql.LevelSerializable {
		// go-ora opens only at the default level; this has to be the
		// transaction's first statement.
		if _, err := tx.ExecContext(ctx, "SET TRANSACTION ISOLATION LEVEL SERIALIZABLE"); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("txsql: set serializable isolation: %w", classify(err))
		}
	}
	return tx, nil
}

// runMessage runs the statement list for one message. results holds what
// each statement returned, by name, for the statements after it; it is per
// message, so one message never sees another's ids. rec, when not nil, is
// told about every statement run; only a simulation passes one.
func (s *Sink) runMessage(ctx context.Context, q sqlutil.Querier, msg hermod.Message, rec *recorder) error {
	results := make(map[string]any, len(s.cfg.Statements))
	fromMsg := evaluator.MessageResolver(msg)

	for i := range s.cfg.Statements {
		st := &s.cfg.Statements[i]
		sc := scope{results: results, fromMsg: fromMsg}

		if st.ForEach == "" {
			res, err := s.runOnce(ctx, q, st, sc, rec.at(st.Name, 0))
			if err != nil {
				return fmt.Errorf("statement %q: %w", st.Name, err)
			}
			results[st.Name] = res
			continue
		}

		items, err := s.items(st, fromMsg)
		if err != nil {
			rec.failed(st.Name, err)
			return fmt.Errorf("statement %q: %w", st.Name, err)
		}
		outs := make([]any, 0, len(items))
		for k, item := range items {
			sc.alias, sc.item = st.As, item
			res, err := s.runOnce(ctx, q, st, sc, rec.at(st.Name, k+1))
			if err != nil {
				return fmt.Errorf("statement %q, item %d: %w", st.Name, k+1, err)
			}
			outs = append(outs, res)
		}
		results[st.Name] = outs
	}
	return nil
}

// items reads a statement's forEach list from the message.
func (s *Sink) items(st *Statement, fromMsg func(string) any) ([]any, error) {
	v := fromMsg(st.ForEach)
	if v == nil {
		if s.cfg.OnUnresolved == OnUnresolvedFail {
			return nil, permanent(fmt.Errorf("the list %q resolved to nothing on this message", st.ForEach))
		}
		return nil, nil
	}
	list, ok := asList(v)
	if !ok {
		return nil, permanent(fmt.Errorf("%q is not a list (it is %T), so there is nothing to run once per item", st.ForEach, v))
	}
	if len(list) > MaxForEachItems {
		return nil, permanent(fmt.Errorf("%q has %d items; at most %d run in one transaction", st.ForEach, len(list), MaxForEachItems))
	}
	return list, nil
}

func asList(v any) ([]any, bool) {
	switch t := v.(type) {
	case []any:
		return t, true
	case string:
		// A list that arrived as JSON text: a text column, or a body that
		// was never decoded.
		if s := strings.TrimSpace(t); strings.HasPrefix(s, "[") {
			var out []any
			if json.Unmarshal([]byte(s), &out) == nil {
				return out, true
			}
		}
		return nil, false
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice || rv.Type().Elem().Kind() == reflect.Uint8 {
		return nil, false
	}
	out := make([]any, rv.Len())
	for i := range out {
		out[i] = rv.Index(i).Interface()
	}
	return out, true
}

// runOnce binds and runs one statement and checks its expectation. It returns
// what later statements can read under its name: the first returned row, or,
// for a statement that returns none, the MySQL insert id when there is one.
func (s *Sink) runOnce(ctx context.Context, q sqlutil.Querier, st *Statement, sc scope, step *stepRecord) (out map[string]any, err error) {
	b := sqlutil.ParameterizeTemplateWith(s.driver, st.SQL, sc.resolve)
	step.bound(b)
	defer func() { step.done(err) }()
	if b.Err != nil {
		return nil, permanent(b.Err)
	}
	if s.cfg.OnUnresolved == OnUnresolvedFail && len(b.Unresolved) > 0 {
		return nil, permanent(fmt.Errorf("template variable(s) resolved to nothing on this message: %s "+
			"(set on_unresolved to null to bind NULL instead)", strings.Join(b.Unresolved, ", ")))
	}

	sctx, cancel := context.WithTimeout(ctx, s.cfg.StatementTimeout)
	defer cancel()
	switch {
	case st.rows:
		return s.query(sctx, q, st, b, step)
	case len(st.outCols) > 0:
		return execOut(sctx, q, st, b, step)
	}
	return s.exec(sctx, q, st, b, step)
}

// query runs a statement that hands rows back and returns the first.
func (s *Sink) query(ctx context.Context, q sqlutil.Querier, st *Statement, b sqlutil.TemplateBinding, step *stepRecord) (map[string]any, error) {
	rows, err := q.QueryContext(ctx, b.SQL, b.Args...)
	if err != nil {
		return nil, classify(err)
	}
	defer func() { _ = rows.Close() }()
	// Read to the end: some drivers report a refused write only once the
	// result is read.
	kept, total, err := sqlutil.ScanRowsCounted(rows)
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		return nil, classify(err)
	}
	if s.driver == "oracle" {
		for i := range kept {
			kept[i] = lowerKeys(kept[i])
		}
	}
	step.returned(kept, total)
	if err := check(st.Expect, total, "returned"); err != nil {
		return nil, err
	}
	if len(kept) == 0 {
		return nil, nil
	}
	return kept[0], nil
}

// exec runs a statement that hands no rows back. What it returns for later
// statements is the MySQL insert id, when there is one.
func (s *Sink) exec(ctx context.Context, q sqlutil.Querier, st *Statement, b sqlutil.TemplateBinding, step *stepRecord) (map[string]any, error) {
	res, err := q.ExecContext(ctx, b.SQL, b.Args...)
	if err != nil {
		return nil, classify(err)
	}
	n, err := res.RowsAffected()
	step.changed(n, err)
	if err != nil && st.Expect != ExpectAny {
		return nil, fmt.Errorf("the driver cannot count changed rows, so expect %q cannot be checked: %w", st.Expect, err)
	}
	if err := check(st.Expect, n, "changed"); err != nil {
		return nil, err
	}
	out := map[string]any{}
	if s.wantsInsertID(st) {
		if id, err := res.LastInsertId(); err == nil && id > 0 {
			out[st.idKey()] = id
		}
	}
	return out, nil
}

// wantsInsertID: MySQL has no RETURNING, so an INSERT's generated key is read
// from the driver instead, unless the statement turned that off.
func (s *Sink) wantsInsertID(st *Statement) bool {
	if s.driver != "mysql" || (st.ReturnID != nil && !*st.ReturnID) {
		return false
	}
	kw := firstKeyword(stripLiterals(st.SQL))
	return kw == "INSERT" || kw == "REPLACE"
}

// check refuses a run that did not do what the statement expects.
func check(expect string, n int64, verb string) error {
	switch expect {
	case ExpectOne:
		if n != 1 {
			return permanent(fmt.Errorf("expected exactly 1 row, but it %s %d rows", verb, n))
		}
	case ExpectAffected:
		if n < 1 {
			return permanent(fmt.Errorf("expected at least 1 row, but it %s 0 rows", verb))
		}
	}
	return nil
}

// scope resolves a token path for one statement run: the current forEach
// item first, then an earlier statement's result, then the message.
type scope struct {
	results map[string]any
	fromMsg func(string) any
	alias   string
	item    any
}

func (sc scope) resolve(path string) any {
	path = strings.TrimPrefix(strings.TrimSpace(path), ".")
	head, rest, _ := strings.Cut(path, ".")
	if sc.alias != "" && head == sc.alias {
		return walk(sc.item, rest)
	}
	if v, ok := sc.results[head]; ok {
		return walk(v, rest)
	}
	return sc.fromMsg(path)
}

// walk reads rest from v. Column names are matched exactly, then without
// regard to case: databases disagree on how they report an unquoted name.
func walk(v any, rest string) any {
	if rest == "" {
		if m, ok := v.(map[string]any); ok && m == nil {
			return nil
		}
		return v
	}
	m, ok := v.(map[string]any)
	if !ok || m == nil {
		return nil
	}
	if got := sqlutil.GetFromMapPath(m, rest); got != nil {
		return got
	}
	head, tail, _ := strings.Cut(rest, ".")
	for k, val := range m {
		if strings.EqualFold(k, head) {
			return walk(val, tail)
		}
	}
	return nil
}
