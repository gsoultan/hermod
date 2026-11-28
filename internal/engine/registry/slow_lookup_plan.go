// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gsoultan/hermod/internal/advisor/queryplan"
	"github.com/gsoultan/hermod/pkg/comm/transformer/lookup"
	"github.com/jackc/pgx/v5/pgconn"
)

// lockNotAvailable is SQLSTATE 55P03: the plan waited past
// queryplan.LockTimeout for a lock on one of the query's tables.
const lockNotAvailable = "55P03"

// lockHoldersNamed is how many sessions a locked verdict names.
const lockHoldersNamed = 3

// PlanSlowLookup reads the plan of a lookup query that ran out of time while
// its database was answering, and says why the query is slow and what would
// fix it. It plans and never runs the query (see queryplan.Run), so it is
// safe on a database that is already struggling. Only PostgreSQL is planned;
// any other database is lookup.ErrUnplannable.
//
// When planning itself has to wait for a lock, the query was waiting on that
// lock and not on its plan, and the verdict names the sessions holding one.
func (r *Registry) PlanSlowLookup(ctx context.Context, db *sql.DB, query string) (string, error) {
	c, err := queryplan.Run(ctx, db, query)
	if errors.Is(err, queryplan.ErrUnsupported) {
		return "", fmt.Errorf("%w: %w", lookup.ErrUnplannable, err)
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == lockNotAvailable {
		return lockedVerdict(ctx, db), nil
	}
	if err != nil {
		return "", err
	}
	return c.Summary(), nil
}

// lockedVerdict names the sessions holding an ACCESS EXCLUSIVE lock on a table
// of this database: the only lock a SELECT waits for. It reports the table,
// the session and how long its transaction has been open -- never its query
// text, which can hold anyone's data.
func lockedVerdict(ctx context.Context, db *sql.DB) string {
	const lead = "even planning it had to wait for a lock on one of its tables, so the query was waiting on a lock, not on its plan"
	rows, err := db.QueryContext(ctx, `
SELECT n.nspname || '.' || c.relname, l.pid, coalesce(a.state, ''), coalesce(extract(epoch FROM now() - a.xact_start), -1)
  FROM pg_locks l
  JOIN pg_class c ON c.oid = l.relation JOIN pg_namespace n ON n.oid = c.relnamespace
  LEFT JOIN pg_stat_activity a ON a.pid = l.pid
 WHERE l.granted AND l.locktype = 'relation' AND l.mode = 'AccessExclusiveLock'
   AND l.database = (SELECT oid FROM pg_database WHERE datname = current_database())
   AND l.pid <> pg_backend_pid()
 ORDER BY a.xact_start NULLS LAST
 LIMIT $1`, lockHoldersNamed)
	if err != nil {
		return lead + " (who holds it could not be read: " + err.Error() + ")"
	}
	defer func() { _ = rows.Close() }()
	var held []string
	for rows.Next() {
		var rel, state string
		var pid int
		var open float64
		if rows.Scan(&rel, &pid, &state, &open) != nil {
			continue
		}
		held = append(held, lockHolder(rel, pid, state, open))
	}
	if err := rows.Err(); err != nil && len(held) == 0 {
		return lead + " (who holds it could not be read: " + err.Error() + ")"
	}
	if len(held) == 0 {
		return lead + "; it was released before Hermod could see who held it"
	}
	return lead + ": " + strings.Join(held, "; ") +
		". Until that transaction ends every lookup on the table waits; end it, or schedule the work that takes it outside busy hours"
}

func lockHolder(rel string, pid int, state string, open float64) string {
	s := fmt.Sprintf("%s is locked ACCESS EXCLUSIVE by session %d", rel, pid)
	var about []string
	if state != "" {
		about = append(about, state)
	}
	if open >= 0 {
		about = append(about, "transaction open "+(time.Duration(open*float64(time.Second))).Round(time.Second).String())
	}
	if len(about) > 0 {
		s += " (" + strings.Join(about, ", ") + ")"
	}
	return s
}
