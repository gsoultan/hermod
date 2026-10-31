// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package sqlutil

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Querier is what a node runs statements on. *sql.DB, *sql.Tx and the preview
// sandbox's transaction all satisfy it.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Tx is a transaction a node opened on a DB.
type Tx interface {
	Querier
	Commit() error
	Rollback() error
}

// DB is a Querier that can also open a transaction: the pool outside a
// preview, the run's sandbox inside one.
type DB interface {
	Querier
	BeginTx(ctx context.Context, opts *sql.TxOptions) (Tx, error)
}

// Use is the DB a node runs its SQL on. Outside a preview it is the pool, and
// nothing changes. Inside one it is the run's transaction on that pool, so a
// node sees what earlier nodes of the same run wrote, and nothing the run
// writes is ever committed.
//
// driver is the source type or the database/sql driver name.
func Use(ctx context.Context, db *sql.DB, driver string) (DB, error) {
	sb := sandboxFrom(ctx)
	if sb == nil {
		return poolDB{db}, nil
	}
	return sb.use(db, driver)
}

type poolDB struct{ *sql.DB }

func (p poolDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (Tx, error) {
	return p.DB.BeginTx(ctx, opts)
}

type sandboxKey struct{}

// WithSandbox puts sb on ctx. Every Use on the returned context goes through it.
func WithSandbox(ctx context.Context, sb *Sandbox) context.Context {
	return context.WithValue(ctx, sandboxKey{}, sb)
}

// Sandboxed reports whether ctx belongs to a preview run. A node consults it
// for the things the sandbox cannot do for it, such as not reading or filling
// a cache that live runs share.
func Sandboxed(ctx context.Context) bool {
	return sandboxFrom(ctx) != nil
}

func sandboxFrom(ctx context.Context) *Sandbox {
	if ctx == nil {
		return nil
	}
	sb, _ := ctx.Value(sandboxKey{}).(*Sandbox)
	return sb
}

// Sandbox is one preview run's view of its databases: a transaction per pool,
// opened the first time a node uses that pool and rolled back when the run
// ends. Nothing a preview writes is committed, and every node of the run sees
// what the nodes before it wrote, because they all share the transaction.
//
// The nodes of a run execute one after another, which is what lets them
// share one connection. A node must close its rows before it returns.
type Sandbox struct {
	// base is what each transaction is bound to. When the request behind the
	// run goes away, database/sql rolls the transaction back on its own.
	base context.Context

	mu     sync.Mutex
	txs    map[*sql.DB]*sandboxTx
	closed bool
}

// NewSandbox starts a run's sandbox. Its transactions are bound to ctx; the
// caller must call Rollback when the run ends.
func NewSandbox(ctx context.Context) *Sandbox {
	return &Sandbox{base: ctx, txs: make(map[*sql.DB]*sandboxTx)}
}

// Rollback ends the run: every transaction is rolled back and its connection
// returned to the pool, or dropped from it if it cannot be put back clean.
// Safe to call more than once.
func (s *Sandbox) Rollback() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	var errs []error
	for db, t := range s.txs {
		errs = append(errs, t.end(s.base))
		delete(s.txs, db)
	}
	return errors.Join(errs...)
}

func (s *Sandbox) use(db *sql.DB, driver string) (DB, error) {
	canonical, ok := CanonicalDriver(driver)
	if !ok {
		canonical = driver
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("preview: this run has ended; its database work was rolled back")
	}
	// ClickHouse has no transaction to roll back. It gets the pool, and the
	// statement guard lets nothing through but reads.
	if canonical == "clickhouse" {
		return &readOnlyDB{db: db, driver: canonical}, nil
	}
	if t, ok := s.txs[db]; ok {
		return t, nil
	}
	t, err := beginSandboxTx(s.base, db, canonical)
	if err != nil {
		return nil, err
	}
	s.txs[db] = t
	return t, nil
}

// lockWait bounds how long a preview waits on a lock a live workflow holds.
// A preview is a person watching a spinner; it should fail fast with the
// database's own lock error rather than queue behind production traffic.
const lockWaitSeconds = 5

type sandboxTx struct {
	driver string
	conn   *sql.Conn
	tx     *sql.Tx
	// reset undoes, on the connection, what beginSandboxTx set for the
	// session, before the connection goes back to the pool.
	reset []string

	// stmtGuard: on PostgreSQL a failed statement aborts the whole
	// transaction, so one node's error would fail every node after it. Each
	// statement runs behind a savepoint, and the next statement rolls back
	// to it if the previous one failed.
	stmtGuard bool
	marked    bool
	depth     int
	seq       int
}

func beginSandboxTx(ctx context.Context, db *sql.DB, driver string) (*sandboxTx, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("preview: open connection: %w", err)
	}
	t := &sandboxTx{driver: driver, conn: conn}

	var session []string
	switch driver {
	case "mysql":
		session = []string{fmt.Sprintf("SET SESSION innodb_lock_wait_timeout = %d", lockWaitSeconds)}
		t.reset = []string{"SET SESSION innodb_lock_wait_timeout = DEFAULT"}
	case "sqlserver":
		session = []string{fmt.Sprintf("SET LOCK_TIMEOUT %d", lockWaitSeconds*1000)}
		t.reset = []string{"SET LOCK_TIMEOUT -1"}
	}
	for _, q := range session {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.discard()
			return nil, fmt.Errorf("preview: %s: %w", q, err)
		}
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.discard()
		return nil, fmt.Errorf("preview: begin transaction: %w", err)
	}
	t.tx = tx

	if driver == "pgx" {
		t.stmtGuard = true
		// LOCAL: scoped to this transaction, gone with the rollback.
		q := fmt.Sprintf("SET LOCAL lock_timeout = '%ds'", lockWaitSeconds)
		if _, err := tx.ExecContext(ctx, q); err != nil {
			_ = tx.Rollback()
			t.discard()
			return nil, fmt.Errorf("preview: %s: %w", q, err)
		}
	}
	return t, nil
}

// end rolls the transaction back and returns the connection to the pool.
func (t *sandboxTx) end(base context.Context) error {
	if err := t.tx.Rollback(); !rolledBack(base, err) {
		t.discard()
		return fmt.Errorf("preview: roll back: %w", err)
	}
	if !t.resetSession() {
		// The connection may still carry the preview's session settings. It
		// must not serve a live workflow like that.
		t.discard()
		return nil
	}
	_ = t.conn.Close()
	return nil
}

// rolledBack reports whether nothing of the transaction can be left after
// Rollback returned err. ErrTxDone: the request's context ended and
// database/sql already rolled it back. Rollback can also race that and fail
// with the context's error; the driver drops the connection, and with it the
// transaction. Nothing was committed either way.
func rolledBack(base context.Context, err error) bool {
	return err == nil || errors.Is(err, sql.ErrTxDone) || base.Err() != nil
}

// resetSession undoes what beginSandboxTx set for the session.
func (t *sandboxTx) resetSession() bool {
	for _, q := range t.reset {
		if _, err := t.conn.ExecContext(context.Background(), q); err != nil {
			return false
		}
	}
	return true
}

// discard closes the connection and keeps it out of the pool.
func (t *sandboxTx) discard() {
	_ = t.conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = t.conn.Close()
}

// before runs ahead of every statement at the outermost level; see stmtGuard.
func (t *sandboxTx) before(ctx context.Context) error {
	if !t.stmtGuard || t.depth > 0 {
		return nil
	}
	if err := t.settle(ctx); err != nil {
		return err
	}
	if _, err := t.tx.ExecContext(ctx, "SAVEPOINT hermod_stmt"); err != nil {
		return fmt.Errorf("preview: %w", err)
	}
	t.marked = true
	return nil
}

// settle closes the previous statement's savepoint: kept when the statement
// succeeded, rolled back to when it left the transaction aborted.
func (t *sandboxTx) settle(ctx context.Context) error {
	if !t.marked {
		return nil
	}
	t.marked = false
	if _, err := t.tx.ExecContext(ctx, "RELEASE SAVEPOINT hermod_stmt"); err == nil {
		return nil
	}
	if _, err := t.tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT hermod_stmt"); err != nil {
		return fmt.Errorf("preview: recover from the previous statement's error: %w", err)
	}
	if _, err := t.tx.ExecContext(ctx, "RELEASE SAVEPOINT hermod_stmt"); err != nil {
		return fmt.Errorf("preview: %w", err)
	}
	return nil
}

func (t *sandboxTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := CheckSandboxStatement(t.driver, query); err != nil {
		return nil, err
	}
	if err := t.before(ctx); err != nil {
		return nil, err
	}
	return t.tx.ExecContext(ctx, query, args...)
}

func (t *sandboxTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := CheckSandboxStatement(t.driver, query); err != nil {
		return nil, err
	}
	if err := t.before(ctx); err != nil {
		return nil, err
	}
	return t.tx.QueryContext(ctx, query, args...)
}

// BeginTx opens a savepoint inside the run's transaction. Its Commit keeps the
// work inside the run; nothing leaves the sandbox until the run is rolled back.
func (t *sandboxTx) BeginTx(ctx context.Context, _ *sql.TxOptions) (Tx, error) {
	if t.depth == 0 {
		if err := t.settle(ctx); err != nil {
			return nil, err
		}
	}
	t.seq++
	name := fmt.Sprintf("hermod_nested_%d", t.seq)
	// name is generated above, never taken from input.
	save := "SAVEPOINT " + name //nolint:gosec // G202: generated identifier
	if t.driver == "sqlserver" {
		save = "SAVE TRANSACTION " + name
	}
	if _, err := t.tx.ExecContext(ctx, save); err != nil {
		return nil, fmt.Errorf("preview: %w", err)
	}
	t.depth++
	return &nestedTx{parent: t, name: name, ctx: ctx}, nil
}

type nestedTx struct {
	parent *sandboxTx
	name   string
	ctx    context.Context
	done   bool
}

func (n *nestedTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return n.parent.ExecContext(ctx, query, args...)
}

func (n *nestedTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return n.parent.QueryContext(ctx, query, args...)
}

func (n *nestedTx) Commit() error {
	if n.done {
		return sql.ErrTxDone
	}
	n.done = true
	n.parent.depth--
	switch n.parent.driver {
	case "sqlserver", "oracle":
		// Neither releases a savepoint; it simply stays until the end.
		return nil
	}
	if _, err := n.parent.tx.ExecContext(n.ctx, "RELEASE SAVEPOINT "+n.name); err != nil {
		return fmt.Errorf("preview: %w", err)
	}
	return nil
}

func (n *nestedTx) Rollback() error {
	if n.done {
		return sql.ErrTxDone
	}
	n.done = true
	n.parent.depth--
	undo := "ROLLBACK TO SAVEPOINT " + n.name //nolint:gosec // G202: generated identifier
	if n.parent.driver == "sqlserver" {
		undo = "ROLLBACK TRANSACTION " + n.name
	}
	if _, err := n.parent.tx.ExecContext(n.ctx, undo); err != nil {
		return fmt.Errorf("preview: %w", err)
	}
	switch n.parent.driver {
	case "sqlserver", "oracle":
		return nil
	}
	if _, err := n.parent.tx.ExecContext(n.ctx, "RELEASE SAVEPOINT "+n.name); err != nil {
		return fmt.Errorf("preview: %w", err)
	}
	return nil
}

// readOnlyDB is an engine with no transaction to undo a write: reads run on
// the pool, anything else is refused by the guard.
type readOnlyDB struct {
	db     *sql.DB
	driver string
}

func (r *readOnlyDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := CheckSandboxStatement(r.driver, query); err != nil {
		return nil, err
	}
	return r.db.ExecContext(ctx, query, args...)
}

func (r *readOnlyDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := CheckSandboxStatement(r.driver, query); err != nil {
		return nil, err
	}
	return r.db.QueryContext(ctx, query, args...)
}

func (r *readOnlyDB) BeginTx(context.Context, *sql.TxOptions) (Tx, error) {
	return nil, fmt.Errorf("preview: %s cannot roll back a write, so a preview does not run one", r.driver)
}

// CheckSandboxStatement refuses a statement that would leave the preview's
// transaction or that the engine cannot roll back. It reads the text outside
// string literals, quoted identifiers and comments.
func CheckSandboxStatement(driver, query string) error {
	canonical, ok := CanonicalDriver(driver)
	if !ok {
		canonical = driver
	}
	stmts := splitStatements(canonical, query)
	if len(stmts) > 1 {
		return errors.New("preview: runs one statement at a time; split this into separate nodes")
	}
	if len(stmts) == 0 {
		return nil
	}
	if err := checkTransactionControl(canonical, stmts[0]); err != nil {
		return err
	}
	return checkEngineCanRollBack(canonical, stmts[0][0])
}

// checkTransactionControl refuses a statement that would commit, end or
// replace the preview's transaction.
func checkTransactionControl(driver string, words []string) error {
	first := words[0]
	for i, w := range words {
		switch w {
		case "COMMIT", "ROLLBACK":
			return fmt.Errorf("preview: %s would end the transaction the preview runs in, so it is not run here", w)
		case "SAVEPOINT", "RELEASE":
			if i == 0 {
				return fmt.Errorf("preview: %s is not run in a preview; the preview manages its own savepoints", w)
			}
		case "TRANSACTION", "TRAN", "WORK":
			if i > 0 && (words[i-1] == "BEGIN" || words[i-1] == "START") {
				return errors.New("preview: a statement that opens its own transaction is not run in a preview")
			}
		case "AUTOCOMMIT", "IMPLICIT_TRANSACTIONS":
			if first == "SET" {
				return fmt.Errorf("preview: changing %s is not run in a preview", w)
			}
		}
	}
	// BEGIN starts a block on SQL Server and Oracle (BEGIN TRY, BEGIN ... END)
	// and a transaction everywhere else.
	if (first == "BEGIN" || first == "END") && driver != "sqlserver" && driver != "oracle" {
		return fmt.Errorf("preview: %s would change the transaction the preview runs in, so it is not run here", first)
	}
	return nil
}

// checkEngineCanRollBack refuses, by its first word, a statement this engine
// would commit on its own.
func checkEngineCanRollBack(driver, first string) error {
	switch driver {
	case "clickhouse":
		switch first {
		case "SELECT", "WITH", "SHOW", "DESCRIBE", "DESC", "EXPLAIN", "EXISTS":
			return nil
		}
		return errors.New("preview: ClickHouse cannot roll back a write, so a preview does not run it")
	case "mysql", "oracle", "snowflake":
		switch first {
		case "CREATE", "ALTER", "DROP", "TRUNCATE", "RENAME", "GRANT", "REVOKE",
			"LOCK", "UNLOCK", "ANALYZE", "OPTIMIZE", "FLUSH", "COMMENT", "PURGE":
			return fmt.Errorf("preview: %s commits on its own on this database, so a preview does not run it", first)
		case "CALL", "EXEC", "EXECUTE", "BEGIN", "DECLARE":
			// A procedure or block can commit inside itself.
			return fmt.Errorf("preview: %s can commit on its own on this database, so a preview does not run it", first)
		}
	}
	return nil
}

// StatementMayWrite reports whether a query could change data. Only text that
// is plainly a read says false; anything else, including text it cannot make
// sense of, says true.
func StatementMayWrite(driver, query string) bool {
	canonical, ok := CanonicalDriver(driver)
	if !ok {
		canonical = driver
	}
	stmts := splitStatements(canonical, query)
	if len(stmts) != 1 {
		return true
	}
	words := stmts[0]
	switch words[0] {
	case "SELECT", "WITH", "SHOW", "DESCRIBE", "DESC", "EXPLAIN", "VALUES", "TABLE":
	default:
		return true
	}
	for _, w := range words {
		switch w {
		case "INSERT", "UPDATE", "DELETE", "MERGE", "INTO", "UPSERT", "REPLACE", "TRUNCATE",
			"CREATE", "ALTER", "DROP", "CALL", "EXEC", "EXECUTE", "COPY", "NEXTVAL", "SETVAL", "LOCK":
			return true
		}
	}
	return false
}

// splitStatements returns the upper-cased words of each non-empty statement in
// query, skipping string literals, quoted identifiers and comments. Statements
// are separated by a semicolon outside any of those.
//
// The dialect matters where a misreading would hide words: a backslash escapes
// a quote only in MySQL, and only PostgreSQL has dollar quoting. Read the
// other way, 'x\'; COMMIT; --' hides a COMMIT inside what looks like a string.
func splitStatements(driver, query string) [][]string {
	var (
		stmts [][]string
		cur   []string
		word  strings.Builder
	)
	flushWord := func() {
		if word.Len() > 0 {
			cur = append(cur, strings.ToUpper(word.String()))
			word.Reset()
		}
	}
	for i := 0; i < len(query); {
		c := query[i]
		if next, ok := skipNonCode(driver, query, i); ok {
			flushWord()
			i = next
			continue
		}
		switch {
		case c == ';':
			flushWord()
			if len(cur) > 0 {
				stmts = append(stmts, cur)
			}
			cur = nil
		case isWordByte(c):
			word.WriteByte(c)
		default:
			flushWord()
		}
		i++
	}
	flushWord()
	if len(cur) > 0 {
		stmts = append(stmts, cur)
	}
	return stmts
}

// skipNonCode reports whether a comment, quoted run or placeholder starts at
// i, and the index just past it.
func skipNonCode(driver, s string, i int) (int, bool) {
	if next, ok := skipComment(s, i); ok {
		return next, true
	}
	switch c := s[i]; {
	case c == '\'' || c == '"' || c == '`':
		return skipQuoted(s, i, c, driver == "mysql"), true
	case c == '[' && driver == "sqlserver":
		return skipQuoted(s, i, ']', false), true
	case c == '$' && driver == "pgx":
		return skipDollar(s, i), true
	}
	return i, false
}

func skipComment(s string, i int) (int, bool) {
	if i+1 >= len(s) {
		return i, false
	}
	switch s[i : i+2] {
	case "--":
		if end := strings.IndexByte(s[i:], '\n'); end >= 0 {
			return i + end, true
		}
		return len(s), true
	case "/*":
		if end := strings.Index(s[i+2:], "*/"); end >= 0 {
			return i + 2 + end + 2, true
		}
		return len(s), true
	}
	return i, false
}

// skipDollar skips a PostgreSQL dollar-quoted string or a $1 placeholder.
func skipDollar(s string, i int) int {
	if tag, ok := dollarTag(s, i); ok {
		if end := strings.Index(s[i+len(tag):], tag); end >= 0 {
			return i + len(tag) + end + len(tag)
		}
		return len(s)
	}
	j := i + 1
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	return j
}

func isWordByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

// skipQuoted returns the index just past the quoted run starting at i and
// ending at closer. A doubled quote is an escaped one.
func skipQuoted(s string, i int, closer byte, backslash bool) int {
	doubled := s[i] == closer
	i++
	for i < len(s) {
		if backslash && s[i] == '\\' {
			i += 2
			continue
		}
		if s[i] == closer {
			if doubled && i+1 < len(s) && s[i+1] == closer {
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return i
}

// dollarTag reads a PostgreSQL dollar-quote opener ($$ or $tag$) at i.
func dollarTag(s string, i int) (string, bool) {
	j := i + 1
	for j < len(s) && (s[j] == '_' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || (j > i+1 && s[j] >= '0' && s[j] <= '9')) {
		j++
	}
	if j < len(s) && s[j] == '$' {
		return s[i : j+1], true
	}
	return "", false
}
