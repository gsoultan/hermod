// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package queryplan

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// ErrUnsupported is returned for a database other than PostgreSQL.
var ErrUnsupported = errors.New("queryplan: only PostgreSQL databases can be checked so far")

// PlanTimeout bounds planning on the server. Planning reads the catalog
// only, so it takes milliseconds; the bound is for a table locked by DDL.
const PlanTimeout = 5 * time.Second

// Check is one query's plan read against its tables.
type Check struct {
	Scans    []Scan    `json:"scans"`
	Tables   []Table   `json:"tables"`
	Findings []Finding `json:"findings"`
}

// Run plans query on db and assesses the plan. It never runs the query:
//
//   - the query is prepared in one extended-protocol message, where the
//     server refuses more than one statement, so "SELECT 1; DROP ..." fails;
//   - only EXPLAIN without ANALYZE is executed, which plans and does not run;
//   - all of it happens in a READ ONLY transaction under a statement timeout,
//     rolled back at the end, with the prepared statement deallocated first
//     (a prepared statement outlives a rollback).
//
// The plan is the generic one, planned without parameter values, because a
// check has no message to take them from. Everything stays in one
// transaction, so it is safe through PgBouncer in transaction mode.
func Run(ctx context.Context, db *sql.DB, query string) (Check, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return Check{}, err
	}
	defer func() { _ = conn.Close() }()
	var out Check
	err = conn.Raw(func(dc any) error {
		sc, ok := dc.(*stdlib.Conn)
		if !ok {
			return ErrUnsupported
		}
		out, err = run(ctx, sc.Conn(), query)
		return err
	})
	return out, err
}

func run(ctx context.Context, c *pgx.Conn, query string) (out Check, err error) {
	pc := c.PgConn()
	if _, err := pc.Exec(ctx, fmt.Sprintf(
		"BEGIN READ ONLY; SET LOCAL statement_timeout = %d; SET LOCAL plan_cache_mode = force_generic_plan",
		PlanTimeout.Milliseconds())).ReadAll(); err != nil {
		return out, err
	}
	defer func() {
		// The session goes back to the pool: it must leave no transaction.
		if _, rbErr := pc.Exec(context.WithoutCancel(ctx), "ROLLBACK").ReadAll(); rbErr != nil && err == nil {
			err = rbErr
		}
	}()

	plan, err := explainGeneric(ctx, c, query)
	if err != nil {
		return out, err
	}
	if out.Scans, err = ParsePlan(plan); err != nil {
		return out, err
	}
	tables, err := loadCatalog(ctx, c, out.Scans)
	if err != nil {
		return out, fmt.Errorf("the table's indexes could not be read: %w", err)
	}
	for _, t := range tables {
		out.Tables = append(out.Tables, t)
	}
	slices.SortFunc(out.Tables, func(a, b Table) int { return strings.Compare(a.Key(), b.Key()) })
	out.Findings = Assess(out.Scans, tables)
	return out, nil
}

// explainGeneric prepares query under a unique name, plans it generically and
// deallocates it. It runs inside run's read-only transaction.
func explainGeneric(ctx context.Context, c *pgx.Conn, query string) (plan []byte, err error) {
	pc := c.PgConn()
	name, err := statementName()
	if err != nil {
		return nil, err
	}
	// SQL-level PREPARE, sent as one extended-protocol statement: the server
	// refuses a second statement in it, and PgBouncer passes it through as
	// is, where it renames protocol-level statements when it tracks them.
	if err := pc.ExecParams(ctx, "PREPARE "+name+" AS "+query, nil, nil, nil, nil).Read().Err; err != nil {
		return nil, fmt.Errorf("the query could not be planned: %w", err)
	}
	defer func() {
		// A prepared statement outlives a rollback: drop it first.
		if dErr := pc.Exec(context.WithoutCancel(ctx), "DEALLOCATE "+name).Close(); dErr != nil && err == nil {
			err = dErr
		}
	}()
	var params int
	if err := c.QueryRow(ctx, "SELECT cardinality(parameter_types) FROM pg_prepared_statements WHERE name = $1",
		pgx.QueryExecModeExec, name).Scan(&params); err != nil {
		return nil, err
	}

	explain := "EXPLAIN (VERBOSE, FORMAT JSON) EXECUTE " + name
	if params > 0 {
		// The values are ignored: force_generic_plan plans without them.
		explain += "(" + strings.Repeat("NULL, ", params-1) + "NULL)"
	}
	res, err := pc.Exec(ctx, explain).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("the query could not be planned: %w", err)
	}
	if len(res) != 1 || len(res[0].Rows) != 1 {
		return nil, errors.New("queryplan: EXPLAIN returned no plan")
	}
	return res[0].Rows[0][0], nil
}

// statementName is unique per check, so two checks on one session through a
// pooler never collide.
func statementName() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "hermod_plan_check_" + hex.EncodeToString(b[:]), nil
}

const catalogQuery = `
SELECT n.nspname, c.relname,
       CASE WHEN c.reltuples < 0 THEN -1 ELSE c.reltuples END::float8,
       i.relname, am.amname, ix.indisunique, pg_get_indexdef(ix.indexrelid),
       ARRAY(SELECT pg_get_indexdef(ix.indexrelid, k, true)
             FROM generate_series(1, ix.indnkeyatts) AS k ORDER BY k)
FROM unnest($1::text[], $2::text[]) AS want(schema, rel)
JOIN pg_namespace n ON n.nspname = want.schema
JOIN pg_class c ON c.relnamespace = n.oid AND c.relname = want.rel
LEFT JOIN pg_index ix ON ix.indrelid = c.oid
LEFT JOIN pg_class i ON i.oid = ix.indexrelid
LEFT JOIN pg_am am ON am.oid = i.relam
ORDER BY 1, 2, 4`

// loadCatalog reads the size and indexes of every table the plan reads.
func loadCatalog(ctx context.Context, c *pgx.Conn, scans []Scan) (map[string]Table, error) {
	var schemas, rels []string
	for _, s := range scans {
		if s.Relation != "" {
			schemas, rels = append(schemas, s.Schema), append(rels, s.Relation)
		}
	}
	out := map[string]Table{}
	if len(rels) == 0 {
		return out, nil
	}
	// QueryExecModeExec uses the unnamed statement: nothing is left prepared,
	// which a pooler in transaction mode needs.
	rows, err := c.Query(ctx, catalogQuery, pgx.QueryExecModeExec, schemas, rels)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t Table
		var name, method, def *string
		var unique *bool
		var keys []string
		if err := rows.Scan(&t.Schema, &t.Name, &t.Rows, &name, &method, &unique, &def, &keys); err != nil {
			return nil, err
		}
		if prev, ok := out[t.Key()]; ok {
			t.Indexes = prev.Indexes
		}
		if name != nil {
			t.Indexes = append(t.Indexes, Index{Name: *name, Method: deref(method), Unique: unique != nil && *unique,
				Definition: deref(def), Keys: keys})
		}
		out[t.Key()] = t
	}
	return out, rows.Err()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
