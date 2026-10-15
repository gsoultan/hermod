// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package mysql

// Two-phase commit for the MySQL sink, using XA transactions.
//
// This is what lets a MySQL (or MariaDB — they share this sink) destination be
// a member of a transactional sink group alongside PostgreSQL. It mirrors the
// PostgreSQL sink's contract (pkg/comm/sink/postgres): the coordinator supplies
// the identifier, Prepare leaves the transaction in doubt on the server,
// CommitPrepared and RollbackPrepared resolve it from any connection, and
// rolling back an identifier the server does not know is success.
//
// What is different about MySQL, and why the code looks the way it does:
//
//   - The identifier is fixed at XA START, not at prepare time, and an XA
//     transaction cannot be renamed. So the sink implements hermod.NamedBegin
//     and the coordinator hands it the ID at Begin; Prepare refuses any other.
//   - An XA transaction lives on one session. Every statement between XA START
//     and XA PREPARE has to run on the same connection, so the sink holds a
//     dedicated *sql.Conn for the duration rather than going through the pool.
//   - XA statements take no bind parameters. The identifier is interpolated,
//     so it is validated first: a canonical UUID, nothing else.
//   - A prepared XA transaction survives the client disconnecting only on
//     MySQL 5.7.7+ and MariaDB 10.5.2+. Earlier servers roll it back on
//     disconnect, which would silently turn a "yes" vote into an abort.
//     PreflightTwoPhaseCommit refuses those servers.
//   - Before MySQL 8.0.27 (and on any server with xa_detach_on_prepare=OFF, and
//     on MariaDB) a prepared XA transaction stays attached to the session that
//     prepared it until that session ends; until then, another connection
//     cannot commit it. Prepare therefore discards the connection rather than
//     returning it to the pool: that detaches the transaction on every version,
//     and it guarantees a pooled connection is never handed out still carrying
//     XA state.
//   - DDL implicitly commits, which MySQL refuses while an XA transaction is
//     ACTIVE. Table creation therefore runs through the pool, outside the
//     transaction (see WriteBatch). MySQL DDL is never transactional, so this
//     gives up nothing.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"strings"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
)

// xaFormatID is the XID format identifier Hermod uses: "HMOD" in ASCII.
//
// MySQL's default is 1. A distinct value puts Hermod's XA transactions in a
// namespace of their own, so XA RECOVER output can be told apart from another
// application's on the same server, and an operator resolving by hand cannot
// touch Hermod's transactions by accident — or Hermod theirs.
const xaFormatID int64 = 0x484D4F44

// erXAUnknownXID is MySQL's ER_XAER_NOTA, "XAER_NOTA: Unknown XID".
const erXAUnknownXID = 1397

// xaTxn is the XA transaction this sink has open.
type xaTxn struct {
	conn *sql.Conn
	xid  string
}

// sqlExecutor is what the write path needs from *sql.Tx (an ordinary batch),
// *sql.Conn (inside an XA transaction) and *sql.DB (DDL outside one).
type sqlExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
}

// xidLiteral renders txID as the XID clause of an XA statement — the global
// transaction ID, an empty branch qualifier, and xaFormatID.
//
// XA statements cannot be prepared, so the identifier is interpolated, and is
// therefore refused unless it is a canonical lower-case UUID. uuid.Parse alone
// is not enough: it accepts braces, a urn: prefix and undashed forms, and an
// identifier that is not byte-for-byte the one recorded in the coordinator's
// log is one recovery cannot match.
func xidLiteral(txID string) (string, error) {
	u, err := uuid.Parse(txID)
	if err != nil || u.String() != txID {
		return "", fmt.Errorf("mysql sink: refusing XA transaction ID %q: it must be a canonical "+
			"lower-case UUID, because it is interpolated into XA statements, which take no bind parameters", txID)
	}
	return fmt.Sprintf("'%s','',%d", txID, xaFormatID), nil
}

// parseRecoveredXID reads one XA RECOVER row and reports the transaction ID if
// the XID is one of Hermod's: its format ID, no branch qualifier, and a UUID
// global transaction ID. Anything else belongs to someone else.
func parseRecoveredXID(formatID, gtridLen, bqualLen int64, data []byte) (string, bool) {
	if formatID != xaFormatID || bqualLen != 0 || gtridLen < 0 || gtridLen > int64(len(data)) {
		return "", false
	}
	gtrid := string(data[:gtridLen])
	if _, err := xidLiteral(gtrid); err != nil {
		return "", false
	}
	return gtrid, true
}

// checkXAServerVersion decides from SELECT VERSION() whether a prepared XA
// transaction will survive the client disconnecting, which is the property
// two-phase commit cannot do without: before it, a crash after the vote rolls
// the transaction back behind the coordinator's back.
//
// checkSupportVar is true for MySQL 5.7.7–5.7.9, where innodb_support_xa still
// exists and can be switched off; from 5.7.10 it is always on (and removed in
// 8.0).
func checkXAServerVersion(version string) (checkSupportVar bool, err error) {
	v := version
	mariadb := strings.Contains(strings.ToLower(v), "mariadb")
	if mariadb {
		// MariaDB reports "5.5.5-10.x.y-MariaDB" to clients that expect a
		// MySQL 5 version; the real one follows the prefix.
		v = strings.TrimPrefix(v, "5.5.5-")
	}
	major, minor, patch, ok := parseVersion(v)
	if !ok {
		return false, fmt.Errorf("mysql sink: cannot parse server version %q, so cannot tell whether "+
			"a prepared XA transaction would survive a disconnect", version)
	}
	at := func(a, b, c int) bool {
		if major != a {
			return major > a
		}
		if minor != b {
			return minor > b
		}
		return patch >= c
	}
	if mariadb {
		if !at(10, 5, 2) {
			return false, fmt.Errorf("mysql sink: MariaDB %s rolls back a prepared XA transaction when the "+
				"client disconnects (fixed in 10.5.2, MDEV-742), so a crash after the vote would abort it "+
				"behind the coordinator's back. Upgrade to MariaDB 10.5.2 or later to use it in a transactional group", version)
		}
		return false, nil
	}
	if !at(5, 7, 7) {
		return false, fmt.Errorf("mysql sink: MySQL %s rolls back a prepared XA transaction when the "+
			"client disconnects (fixed in 5.7.7), so a crash after the vote would abort it behind the "+
			"coordinator's back. Upgrade to MySQL 5.7.7 or later (8.0.30+ if binary logging is on) to use it "+
			"in a transactional group", version)
	}
	return !at(5, 7, 10), nil
}

// parseVersion reads the leading "major.minor.patch" of a server version.
func parseVersion(v string) (major, minor, patch int, ok bool) {
	if i := strings.IndexFunc(v, func(r rune) bool { return r != '.' && (r < '0' || r > '9') }); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) < 3 {
		return 0, 0, 0, false
	}
	var nums [3]int
	for i := range 3 {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return 0, 0, 0, false
		}
		nums[i] = n
	}
	return nums[0], nums[1], nums[2], true
}

// isXAUnknown reports whether err is MySQL saying the XID does not exist.
func isXAUnknown(err error) bool {
	var myErr *gomysql.MySQLError
	return errors.As(err, &myErr) && myErr.Number == erXAUnknownXID
}

// discardConn closes conn's physical connection instead of returning it to the
// pool. On the server that ends the session: an XA transaction that was not
// prepared is rolled back, and one that was is detached and survives.
func discardConn(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
}

func (s *MySQLSink) currentXA() *xaTxn {
	s.xaMu.Lock()
	defer s.xaMu.Unlock()
	return s.xa
}

func (s *MySQLSink) setXA(xa *xaTxn) {
	s.xaMu.Lock()
	defer s.xaMu.Unlock()
	s.xa = xa
}

// takeXA clears and returns the open XA transaction, so only one caller ends it.
func (s *MySQLSink) takeXA() *xaTxn {
	s.xaMu.Lock()
	defer s.xaMu.Unlock()
	xa := s.xa
	s.xa = nil
	return xa
}

func (s *MySQLSink) getDB(ctx context.Context) (*sql.DB, error) {
	if err := s.init(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, errors.New("mysql sink: closed")
	}
	return s.db, nil
}

// Begin opens an XA transaction under an identifier of the sink's own choosing.
// The coordinator calls BeginNamed instead, so that the identifier it records
// is the one in force; Begin keeps the sink usable as a plain
// hermod.Transactional.
func (s *MySQLSink) Begin(ctx context.Context) error {
	return s.BeginNamed(ctx, uuid.NewString())
}

// BeginNamed opens an XA transaction under txID on a dedicated connection.
// Subsequent writes join it until Prepare, Commit or Rollback.
func (s *MySQLSink) BeginNamed(ctx context.Context, txID string) error {
	lit, err := xidLiteral(txID)
	if err != nil {
		return err
	}
	if s.currentXA() != nil {
		return errors.New("mysql sink: an XA transaction is already open on this sink")
	}
	db, err := s.getDB(ctx)
	if err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("mysql sink: cannot take a connection for the XA transaction: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "XA START "+lit); err != nil {
		discardConn(conn)
		return fmt.Errorf("mysql sink: XA START: %w", err)
	}
	s.setXA(&xaTxn{conn: conn, xid: txID})
	return nil
}

// Commit commits the open XA transaction in one phase, for a caller using the
// sink as a plain hermod.Transactional. The coordinator never calls it.
func (s *MySQLSink) Commit(ctx context.Context) error {
	xa := s.takeXA()
	if xa == nil {
		return errors.New("no active transaction")
	}
	lit, err := xidLiteral(xa.xid)
	if err != nil {
		discardConn(xa.conn)
		return err
	}
	if _, err := xa.conn.ExecContext(ctx, "XA END "+lit); err != nil {
		discardConn(xa.conn)
		return fmt.Errorf("mysql sink: XA END: %w", err)
	}
	if _, err := xa.conn.ExecContext(ctx, "XA COMMIT "+lit+" ONE PHASE"); err != nil {
		discardConn(xa.conn)
		return fmt.Errorf("mysql sink: XA COMMIT ONE PHASE: %w", err)
	}
	return xa.conn.Close()
}

// Rollback abandons an XA transaction that has not been prepared. With nothing
// open it is a no-op, as on PostgreSQL: the coordinator rolls back every
// participant on an early failure, including ones it never managed to begin.
//
// If the XA statements fail the connection is discarded, and the server rolls
// back an unprepared XA transaction when its session ends — but the error is
// still returned, because the rollback was not confirmed.
func (s *MySQLSink) Rollback(ctx context.Context) error {
	xa := s.takeXA()
	if xa == nil {
		return nil
	}
	lit, err := xidLiteral(xa.xid)
	if err != nil {
		discardConn(xa.conn)
		return err
	}
	if _, err := xa.conn.ExecContext(ctx, "XA END "+lit); err != nil {
		discardConn(xa.conn)
		return fmt.Errorf("mysql sink: XA END during rollback (connection discarded, which rolls back "+
			"an unprepared XA transaction): %w", err)
	}
	if _, err := xa.conn.ExecContext(ctx, "XA ROLLBACK "+lit); err != nil {
		discardConn(xa.conn)
		return fmt.Errorf("mysql sink: XA ROLLBACK (connection discarded, which rolls back "+
			"an unprepared XA transaction): %w", err)
	}
	return xa.conn.Close()
}

// Prepare votes yes: XA END then XA PREPARE, leaving the transaction in doubt on
// the server, where it survives this process and the connection.
//
// txID must be the identifier the transaction was opened under. An XA
// transaction cannot be renamed, so a different one is refused rather than
// recorded: the log would then hold a name nothing on the server carries. An
// empty txID means "whatever it was opened under", for a caller that used Begin.
func (s *MySQLSink) Prepare(ctx context.Context, txID string) (string, error) {
	xa := s.currentXA()
	if xa == nil {
		return "", errors.New("no active transaction")
	}
	if txID == "" {
		txID = xa.xid
	}
	if txID != xa.xid {
		return "", fmt.Errorf("mysql sink: asked to prepare under %q, but the XA transaction was opened "+
			"under %q and cannot be renamed; the coordinator must supply the ID through BeginNamed", txID, xa.xid)
	}
	lit, err := xidLiteral(txID)
	if err != nil {
		return "", err
	}

	s.takeXA()
	if _, err := xa.conn.ExecContext(ctx, "XA END "+lit); err != nil {
		discardConn(xa.conn)
		return "", fmt.Errorf("mysql sink: XA END: %w", err)
	}
	if _, err := xa.conn.ExecContext(ctx, "XA PREPARE "+lit); err != nil {
		// Whether or not the server prepared it, the name is already in the
		// coordinator's log, so the abort that follows can roll it back.
		discardConn(xa.conn)
		return "", fmt.Errorf("mysql sink: XA PREPARE: %w", err)
	}
	// Discard rather than return to the pool; see the package comment. This is
	// what makes the prepared transaction resolvable from another connection on
	// servers that keep it attached to its session.
	discardConn(xa.conn)
	return txID, nil
}

// CommitPrepared commits a prepared XA transaction from any connection — after a
// restart, from a different process than the one that prepared it.
func (s *MySQLSink) CommitPrepared(ctx context.Context, txID string) error {
	lit, err := xidLiteral(txID)
	if err != nil {
		return err
	}
	db, err := s.getDB(ctx)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "XA COMMIT "+lit); err != nil {
		return fmt.Errorf("mysql sink: XA COMMIT: %w", err)
	}
	return nil
}

// RollbackPrepared rolls back a prepared XA transaction from any connection.
//
// An identifier the server does not know is success, for the same reason as on
// PostgreSQL: recovery must be able to run twice, and the coordinator records
// a name before the transaction exists, so a crash in between leaves a name
// with nothing behind it. Reporting that as an error would strand the record
// and keep recovery from reaching participants that do have something
// prepared.
func (s *MySQLSink) RollbackPrepared(ctx context.Context, txID string) error {
	lit, err := xidLiteral(txID)
	if err != nil {
		return err
	}
	db, err := s.getDB(ctx)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "XA ROLLBACK "+lit); err != nil {
		if isXAUnknown(err) {
			return nil
		}
		return fmt.Errorf("mysql sink: XA ROLLBACK: %w", err)
	}
	return nil
}

// preparedXIDs lists Hermod's XA transactions currently prepared on the server,
// from XA RECOVER — MySQL's counterpart of pg_prepared_xacts.
func (s *MySQLSink) preparedXIDs(ctx context.Context) ([]string, error) {
	db, err := s.getDB(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, "XA RECOVER")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var formatID, gtridLen, bqualLen int64
		var data []byte
		if err := rows.Scan(&formatID, &gtridLen, &bqualLen, &data); err != nil {
			return nil, err
		}
		if id, ours := parseRecoveredXID(formatID, gtridLen, bqualLen, data); ours {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}

// PreflightTwoPhaseCommit refuses, at start-up, a server on which a vote could
// not be honoured:
//
//   - a server version that rolls back a prepared XA transaction when the
//     client disconnects (MySQL before 5.7.7, MariaDB before 10.5.2);
//   - MySQL 5.7.7–5.7.9 with innodb_support_xa switched off;
//   - an account that cannot run XA RECOVER (MySQL 8.0 requires the
//     XA_RECOVER_ADMIN privilege), which is what an operator uses to find a
//     transaction left in doubt.
func (s *MySQLSink) PreflightTwoPhaseCommit(ctx context.Context) error {
	db, err := s.getDB(ctx)
	if err != nil {
		return err
	}
	var version string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return fmt.Errorf("mysql sink: cannot read the server version: %w", err)
	}
	checkSupportVar, err := checkXAServerVersion(version)
	if err != nil {
		return err
	}
	if checkSupportVar {
		var on int
		if err := db.QueryRowContext(ctx, "SELECT @@innodb_support_xa").Scan(&on); err != nil {
			return fmt.Errorf("mysql sink: cannot read innodb_support_xa: %w", err)
		}
		if on == 0 {
			return errors.New("mysql sink: innodb_support_xa is OFF, so XA PREPARE is not crash-safe. " +
				"Turn it on, or take this sink out of the transactional group")
		}
	}
	if _, err := s.preparedXIDs(ctx); err != nil {
		return fmt.Errorf("mysql sink: XA RECOVER failed, so a transaction left in doubt could not be "+
			"found (MySQL 8.0+ needs the XA_RECOVER_ADMIN privilege): %w", err)
	}
	return nil
}
