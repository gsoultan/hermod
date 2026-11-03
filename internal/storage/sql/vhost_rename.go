// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
)

// vhostNameTables hold the name of the vhost a row belongs to in a vhost
// column and nowhere else, so a rename is one UPDATE each.
var vhostNameTables = []string{
	"workflows", "sources", "sinks", "dead_letters", "dashboard_history",
	"ml_jobs", "ml_job_results", "ml_feedback", "ml_prediction_logs",
	"ml_quotas", "ai_budgets",
}

// vhostKeyedTable is a table whose id is built from the vhost's name, so a
// rename rewrites the id as well: columns are read for each row of the old
// vhost, and newID builds the row's id under the new name from them.
type vhostKeyedTable struct {
	table   string
	columns []string
	newID   func(vhost string, cols []sql.NullString) (string, error)
}

var vhostKeyedTables = []vhostKeyedTable{
	{"vhost_secrets", []string{"name"}, func(v string, c []sql.NullString) (string, error) {
		return vhostSecretID(v, c[0].String), nil
	}},
	{"ml_models", []string{"name"}, func(v string, c []sql.NullString) (string, error) {
		return mlModelID(v, c[0].String), nil
	}},
	{"ml_scripts", []string{"name", "version"}, func(v string, c []sql.NullString) (string, error) {
		var version int
		if _, err := fmt.Sscan(c[1].String, &version); err != nil {
			return "", fmt.Errorf("script %q has version %q: %w", c[0].String, c[1].String, err)
		}
		return mlScriptID(v, c[0].String, version), nil
	}},
	{"ml_blobs", []string{"blob_key", "chunk"}, func(v string, c []sql.NullString) (string, error) {
		var chunk int
		if _, err := fmt.Sscan(c[1].String, &chunk); err != nil {
			return "", fmt.Errorf("blob %q has chunk %q: %w", c[0].String, c[1].String, err)
		}
		return mlBlobID(v, c[0].String, chunk), nil
	}},
	{"ml_schedules", []string{"kind", "name"}, func(v string, c []sql.NullString) (string, error) {
		return mlScheduleID(v, c[0].String, c[1].String), nil
	}},
	{"ai_usage", []string{"period", "workflow_id"}, func(v string, c []sql.NullString) (string, error) {
		return storage.AIUsageID(v, c[0].String, c[1].String), nil
	}},
}

// txExec runs query in tx after the same placeholder rewrite s.exec does.
// Every statement in this file is built from literals in this file; values are
// bound parameters.
func (s *sqlStorage) txExec(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	_, err := tx.ExecContext(ctx, s.prepareQuery(query), args...) //nolint:gosec // query is built from literals in this file; values are bound.
	return err
}

// txQuery is txExec for a statement that returns rows.
func (s *sqlStorage) txQuery(ctx context.Context, tx *sql.Tx, query string, args ...any) (*sql.Rows, error) {
	return tx.QueryContext(ctx, s.prepareQuery(query), args...) //nolint:gosec // query is built from literals in this file; values are bound.
}

// UpdateVHost saves the vhost's name and description. Everything a vhost holds
// -- its workflows, sources, sinks, users' access, secrets, ML data, AI budget,
// dead letters and dashboard history -- is kept by the vhost's name rather
// than its id, so a new name is carried to all of it in the same transaction
// as the vhost itself: either the vhost and everything in it have the new
// name, or nothing changed.
func (s *sqlStorage) UpdateVHost(ctx context.Context, vhost storage.VHost) error {
	current, err := s.GetVHost(ctx, vhost.ID)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("updating vhost %q: %w", current.Name, err)
	}
	defer func() { _ = tx.Rollback() }()

	if vhost.Name != current.Name {
		var taken int
		if err := tx.QueryRowContext(ctx, s.prepareQuery("SELECT COUNT(*) FROM vhosts WHERE name = ? AND id <> ?"), //nolint:gosec // a constant statement; values are bound.
			vhost.Name, vhost.ID).Scan(&taken); err != nil {
			return fmt.Errorf("renaming vhost %q: %w", current.Name, err)
		}
		if taken > 0 {
			return fmt.Errorf("renaming vhost %q to %q: %w", current.Name, vhost.Name, storage.ErrVHostNameTaken)
		}
	}

	if err := s.txExec(ctx, tx, s.queries.get(QueryUpdateVHost), vhost.Name, vhost.Description, vhost.ID); err != nil {
		return fmt.Errorf("updating vhost %q: %w", current.Name, err)
	}

	if vhost.Name != current.Name && current.Name != "" {
		if err := s.renameVHostContents(ctx, tx, current.Name, vhost.Name); err != nil {
			return fmt.Errorf("renaming vhost %q to %q: %w", current.Name, vhost.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("updating vhost %q: %w", current.Name, err)
	}
	return nil
}

// renameVHostContents moves everything kept under the vhost name from to the
// name to, inside tx.
func (s *sqlStorage) renameVHostContents(ctx context.Context, tx *sql.Tx, from, to string) error {
	for _, table := range vhostNameTables {
		if err := s.txExec(ctx, tx, "UPDATE "+table+" SET vhost = ? WHERE vhost = ?", to, from); err != nil {
			return fmt.Errorf("moving %s: %w", table, err)
		}
	}
	for _, kt := range vhostKeyedTables {
		if err := s.renameVHostKeyedRows(ctx, tx, kt, from, to); err != nil {
			return err
		}
	}
	return s.renameVHostInUsers(ctx, tx, from, to)
}

func (s *sqlStorage) renameVHostKeyedRows(ctx context.Context, tx *sql.Tx, kt vhostKeyedTable, from, to string) error {
	moves, err := s.vhostKeyedMoves(ctx, tx, kt, from, to)
	if err != nil {
		return err
	}
	for oldID, newID := range moves {
		if err := s.txExec(ctx, tx, "UPDATE "+kt.table+" SET id = ?, vhost = ? WHERE id = ?", newID, to, oldID); err != nil {
			return fmt.Errorf("moving %s %s: %w", kt.table, oldID, err)
		}
	}
	return nil
}

// vhostKeyedMoves maps the id of every row of kt under the vhost from to the
// id it has under to. The rows are read in full before any is rewritten.
func (s *sqlStorage) vhostKeyedMoves(ctx context.Context, tx *sql.Tx, kt vhostKeyedTable, from, to string) (map[string]string, error) {
	rows, err := s.txQuery(ctx, tx, "SELECT id, "+strings.Join(kt.columns, ", ")+" FROM "+kt.table+" WHERE vhost = ?", from)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", kt.table, err)
	}
	defer func() { _ = rows.Close() }()

	moves := map[string]string{}
	for rows.Next() {
		var id string
		vals := make([]sql.NullString, len(kt.columns))
		targets := []any{&id}
		for i := range vals {
			targets = append(targets, &vals[i])
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, fmt.Errorf("reading %s: %w", kt.table, err)
		}
		newID, err := kt.newID(to, vals)
		if err != nil {
			return nil, fmt.Errorf("moving %s: %w", kt.table, err)
		}
		moves[id] = newID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", kt.table, err)
	}
	return moves, nil
}

// renameVHostInUsers replaces from with to in every user's vhost list. The list
// is a JSON array in one column, so it is read and rewritten in Go; there are
// as many rows as users, not as messages.
func (s *sqlStorage) renameVHostInUsers(ctx context.Context, tx *sql.Tx, from, to string) error {
	changes, err := s.renamedUserVHosts(ctx, tx, from, to)
	if err != nil {
		return err
	}
	for id, vhosts := range changes {
		if err := s.txExec(ctx, tx, "UPDATE users SET vhosts = ? WHERE id = ?", vhosts, id); err != nil {
			return fmt.Errorf("updating the vhosts of user %s: %w", id, err)
		}
	}
	return nil
}

// renamedUserVHosts maps each user whose vhost list holds from to that list,
// JSON-encoded, with from renamed to.
func (s *sqlStorage) renamedUserVHosts(ctx context.Context, tx *sql.Tx, from, to string) (map[string]string, error) {
	rows, err := s.txQuery(ctx, tx, "SELECT id, vhosts FROM users")
	if err != nil {
		return nil, fmt.Errorf("reading users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	changes := map[string]string{}
	for rows.Next() {
		var id string
		var raw sql.NullString
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, fmt.Errorf("reading users: %w", err)
		}
		if raw.String == "" {
			continue
		}
		var vhosts []string
		if err := json.Unmarshal([]byte(raw.String), &vhosts); err != nil {
			return nil, fmt.Errorf("user %s has an unreadable vhost list: %w", id, err)
		}
		renamed, ok := storage.RenameInVHostList(vhosts, from, to)
		if !ok {
			continue
		}
		encoded, err := json.Marshal(renamed)
		if err != nil {
			return nil, fmt.Errorf("encoding the vhosts of user %s: %w", id, err)
		}
		changes[id] = string(encoded)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading users: %w", err)
	}
	return changes, nil
}
