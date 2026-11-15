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

// Two-phase commit, so a Transactional SQL sink can be a member of a
// transactional group and commit together with other databases.
//
// It follows the contract the PostgreSQL and MySQL sinks already keep (see
// hermod.TwoPhaseCommit and those sinks' xa.go and postgres.go):
//
//   - The coordinator names the transaction when it begins it (NamedBegin), so
//     it can write the name down before anything is prepared. Prepare refuses
//     any other name.
//   - On PostgreSQL and YugabyteDB, Prepare is PREPARE TRANSACTION. It cannot
//     work through a transaction pooler, and it fails when
//     max_prepared_transactions is 0; the preflight refuses both at start-up.
//   - On MySQL and MariaDB, the transaction is an XA transaction on one
//     dedicated connection, which Prepare discards so the prepared transaction
//     can be resolved from any connection. The preflight refuses a server that
//     would roll a prepared XA transaction back on disconnect.
//   - Every identifier is a canonical UUID, because PREPARE TRANSACTION and the
//     XA statements take no bind parameters and the name is interpolated.
//   - Rolling back a name the database does not know is success: the name is
//     recorded before the transaction exists, and recovery must be able to run
//     twice.
//
// SQL Server, SQLite and Oracle can run inside Begin and Commit, but they
// cannot hold a transaction in doubt, so they cannot prepare, and the preflight
// refuses them as group members.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	sinkmysql "github.com/gsoultan/hermod/pkg/comm/sink/mysql"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	_ hermod.TwoPhaseCommit          = (*Sink)(nil)
	_ hermod.NamedBegin              = (*Sink)(nil)
	_ hermod.TwoPhaseCommitPreflight = (*Sink)(nil)
)

// TwoPhaseDrivers are the drivers on which the sink can prepare a
// transaction, and so join a transactional group.
var TwoPhaseDrivers = []string{"postgres", "yugabyte", "mysql", "mariadb"}

// SupportsTwoPhaseCommit reports whether a sink on driver can prepare a
// transaction.
func SupportsTwoPhaseCommit(driver string) bool {
	switch (Config{Driver: driver}).canonicalDriver() {
	case "pgx", "mysql":
		return true
	}
	return false
}

// openTx is the transaction Begin opened. On MySQL it is an XA transaction on
// conn; everywhere else it is tx.
type openTx struct {
	tx   *sql.Tx
	conn *sql.Conn
	xid  string
}

func (o *openTx) querier() sqlutil.Querier {
	if o.conn != nil {
		return o.conn
	}
	return o.tx
}

// openQuerier is where a write runs while Begin's transaction is open, or nil.
func (s *Sink) openQuerier() sqlutil.Querier {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open == nil {
		return nil
	}
	return s.open.querier()
}

// take clears and returns the open transaction, so only one caller ends it.
func (s *Sink) take() *openTx {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.open
	s.open = nil
	return o
}

func (s *Sink) current() *openTx {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open
}

// checkTxID refuses anything but a canonical lower-case UUID: the identifier
// is interpolated into statements that take no bind parameters.
func checkTxID(txID string) error {
	u, err := uuid.Parse(txID)
	if err != nil || u.String() != txID {
		return fmt.Errorf("txsql: refusing transaction ID %q: it must be a canonical lower-case UUID, "+
			"because it is written into PREPARE TRANSACTION and XA statements, which take no bind parameters", txID)
	}
	return nil
}

// Begin opens a transaction under an identifier of the sink's own choosing.
// The coordinator calls BeginNamed instead.
func (s *Sink) Begin(ctx context.Context) error {
	return s.BeginNamed(ctx, uuid.NewString())
}

// BeginNamed opens a transaction under txID. Writes join it until Prepare,
// Commit or Rollback.
func (s *Sink) BeginNamed(ctx context.Context, txID string) error {
	if err := checkTxID(txID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open != nil {
		return errors.New("txsql: a transaction is already open on this sink")
	}
	if s.driver == "mysql" {
		conn, err := s.startXA(ctx, txID)
		if err != nil {
			return err
		}
		s.open = &openTx{conn: conn, xid: txID}
		return nil
	}
	tx, err := s.beginTx(ctx)
	if err != nil {
		return err
	}
	s.open = &openTx{tx: tx, xid: txID}
	return nil
}

var mysqlIsolation = map[sql.IsolationLevel]string{
	sql.LevelReadUncommitted: "READ UNCOMMITTED",
	sql.LevelReadCommitted:   "READ COMMITTED",
	sql.LevelRepeatableRead:  "REPEATABLE READ",
	sql.LevelSerializable:    "SERIALIZABLE",
}

// startXA opens an XA transaction on a dedicated connection. XA START takes
// no isolation level, so the level is set for the connection's next
// transaction first.
func (s *Sink) startXA(ctx context.Context, txID string) (*sql.Conn, error) {
	lit, err := sinkmysql.XIDLiteral(txID)
	if err != nil {
		return nil, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("txsql: cannot take a connection for the XA transaction: %w", classify(err))
	}
	if level, ok := mysqlIsolation[s.cfg.Isolation]; ok {
		if _, err := conn.ExecContext(ctx, "SET TRANSACTION ISOLATION LEVEL "+level); err != nil {
			sinkmysql.DiscardConn(conn)
			return nil, fmt.Errorf("txsql: set isolation: %w", classify(err))
		}
	}
	if _, err := conn.ExecContext(ctx, "XA START "+lit); err != nil {
		sinkmysql.DiscardConn(conn)
		return nil, fmt.Errorf("txsql: XA START: %w", classify(err))
	}
	return conn, nil
}

// Commit commits the open transaction in one phase, for a caller using the
// sink as a plain hermod.Transactional. The coordinator never calls it.
func (s *Sink) Commit(ctx context.Context) error {
	o := s.take()
	if o == nil {
		return errors.New("txsql: no transaction is open")
	}
	if o.conn == nil {
		if err := o.tx.Commit(); err != nil {
			return fmt.Errorf("txsql: commit: %w", classify(err))
		}
		return nil
	}
	lit, err := sinkmysql.XIDLiteral(o.xid)
	if err != nil {
		sinkmysql.DiscardConn(o.conn)
		return err
	}
	for _, stmt := range []string{"XA END " + lit, "XA COMMIT " + lit + " ONE PHASE"} {
		if _, err := o.conn.ExecContext(ctx, stmt); err != nil {
			sinkmysql.DiscardConn(o.conn)
			return fmt.Errorf("txsql: %s: %w", stmt[:len(stmt)-len(lit)-1], classify(err))
		}
	}
	return o.conn.Close()
}

// Rollback abandons a transaction that has not been prepared. With nothing
// open it is a no-op: the coordinator rolls back every participant after an
// early failure, including ones it never began.
func (s *Sink) Rollback(ctx context.Context) error {
	o := s.take()
	if o == nil {
		return nil
	}
	return s.abandon(ctx, o)
}

// abandon rolls o back. On MySQL a failed XA statement discards the
// connection, which rolls back an unprepared XA transaction when its session
// ends, but the error is still returned: the rollback was not confirmed.
func (s *Sink) abandon(ctx context.Context, o *openTx) error {
	if o.conn == nil {
		if err := o.tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			return fmt.Errorf("txsql: rollback: %w", err)
		}
		return nil
	}
	lit, err := sinkmysql.XIDLiteral(o.xid)
	if err != nil {
		sinkmysql.DiscardConn(o.conn)
		return err
	}
	for _, stmt := range []string{"XA END " + lit, "XA ROLLBACK " + lit} {
		if _, err := o.conn.ExecContext(ctx, stmt); err != nil {
			sinkmysql.DiscardConn(o.conn)
			return fmt.Errorf("txsql: %s (connection discarded, which rolls back an unprepared XA transaction): %w",
				stmt[:len(stmt)-len(lit)-1], err)
		}
	}
	return o.conn.Close()
}

// Prepare votes yes: the open transaction is left in doubt on the database,
// where it survives this process and the connection, under txID.
func (s *Sink) Prepare(ctx context.Context, txID string) (string, error) {
	o := s.current()
	if o == nil {
		return "", errors.New("txsql: no transaction is open")
	}
	if txID == "" {
		txID = o.xid
	}
	if txID != o.xid {
		return "", fmt.Errorf("txsql: asked to prepare under %q, but the transaction was opened under %q; "+
			"the coordinator must supply the ID through BeginNamed", txID, o.xid)
	}
	if err := checkTxID(txID); err != nil {
		return "", err
	}
	s.take()

	switch s.driver {
	case "pgx":
		if s.pooled {
			_ = o.tx.Rollback()
			return "", errPooled
		}
		if _, err := o.tx.ExecContext(ctx, fmt.Sprintf("PREPARE TRANSACTION '%s'", txID)); err != nil {
			// A failed PREPARE aborts the transaction, so nothing it wrote stands.
			_ = o.tx.Rollback()
			return "", fmt.Errorf("txsql: PREPARE TRANSACTION: %w", classify(err))
		}
		// PREPARE TRANSACTION ended the transaction on the server; this only
		// hands the connection back to the pool (the ROLLBACK is a no-op).
		_ = o.tx.Rollback()
		return txID, nil
	case "mysql":
		lit, err := sinkmysql.XIDLiteral(txID)
		if err != nil {
			sinkmysql.DiscardConn(o.conn)
			return "", err
		}
		for _, stmt := range []string{"XA END " + lit, "XA PREPARE " + lit} {
			if _, err := o.conn.ExecContext(ctx, stmt); err != nil {
				// The name is already in the coordinator's log, so the abort
				// that follows can roll back whatever the server kept.
				sinkmysql.DiscardConn(o.conn)
				return "", fmt.Errorf("txsql: %s: %w", stmt[:len(stmt)-len(lit)-1], classify(err))
			}
		}
		// Discarded, not returned to the pool: that detaches the prepared
		// transaction, so another connection can resolve it.
		sinkmysql.DiscardConn(o.conn)
		return txID, nil
	}
	_ = s.abandon(ctx, o)
	return "", s.noTwoPhase()
}

// CommitPrepared commits a prepared transaction from any connection.
func (s *Sink) CommitPrepared(ctx context.Context, txID string) error {
	if err := checkTxID(txID); err != nil {
		return err
	}
	switch s.driver {
	case "pgx":
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("COMMIT PREPARED '%s'", txID)); err != nil {
			return fmt.Errorf("txsql: COMMIT PREPARED: %w", err)
		}
		return nil
	case "mysql":
		lit, err := sinkmysql.XIDLiteral(txID)
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, "XA COMMIT "+lit); err != nil {
			return fmt.Errorf("txsql: XA COMMIT: %w", err)
		}
		return nil
	}
	return s.noTwoPhase()
}

// RollbackPrepared rolls back a prepared transaction from any connection. A
// name the database does not know is success; see the notes at the top.
func (s *Sink) RollbackPrepared(ctx context.Context, txID string) error {
	if err := checkTxID(txID); err != nil {
		return err
	}
	switch s.driver {
	case "pgx":
		_, err := s.db.ExecContext(ctx, fmt.Sprintf("ROLLBACK PREPARED '%s'", txID))
		var pgErr *pgconn.PgError
		if err != nil && errors.As(err, &pgErr) && pgErr.Code == "42704" {
			return nil
		}
		if err != nil {
			return fmt.Errorf("txsql: ROLLBACK PREPARED: %w", err)
		}
		return nil
	case "mysql":
		lit, err := sinkmysql.XIDLiteral(txID)
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, "XA ROLLBACK "+lit); err != nil {
			if sinkmysql.IsXAUnknownXID(err) {
				return nil
			}
			return fmt.Errorf("txsql: XA ROLLBACK: %w", err)
		}
		return nil
	}
	// This database never prepares anything, so nothing stands under any
	// name: the rollback has nothing left to do.
	return nil
}

var errPooled = errors.New("txsql: two-phase commit is not possible through a transaction pooler " +
	"(a prepared transaction must be resolved on the backend that created it). " +
	"Connect this sink directly to PostgreSQL, or take it out of the transaction")

func (s *Sink) noTwoPhase() error {
	return fmt.Errorf("txsql: a %s sink cannot hold a transaction in doubt, so it cannot commit together with "+
		"other databases; only these can: postgres, yugabyte, mysql, mariadb", s.cfg.Driver)
}

// PreflightTwoPhaseCommit refuses, at start-up, a sink whose vote could not
// be honoured.
func (s *Sink) PreflightTwoPhaseCommit(ctx context.Context) error {
	switch s.driver {
	case "pgx":
		if s.pooled {
			return errPooled
		}
		var maxPrepared int
		if err := s.db.QueryRowContext(ctx,
			"SELECT current_setting('max_prepared_transactions')::int").Scan(&maxPrepared); err != nil {
			return fmt.Errorf("txsql: cannot read max_prepared_transactions: %w", err)
		}
		if maxPrepared == 0 {
			return errors.New("txsql: max_prepared_transactions is 0, so PREPARE TRANSACTION will fail. " +
				"Set it to at least the number of concurrent transactions across databases (it needs a server restart), " +
				"and read the operational hazard note in docs/transactional-sink-groups.md first: a prepared " +
				"transaction holds locks and blocks VACUUM until it is resolved")
		}
		return nil
	case "mysql":
		if err := sinkmysql.PreflightXA(ctx, s.db); err != nil {
			return fmt.Errorf("txsql: %w", err)
		}
		return nil
	}
	return s.noTwoPhase()
}
