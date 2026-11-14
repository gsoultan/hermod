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
	"time"
)

// PurgeAuditLogsForVHost deletes one vhost's audit rows older than before. Rows
// of other vhosts, and rows that belong to none, are not touched.
func (s *sqlStorage) PurgeAuditLogsForVHost(ctx context.Context, vhost string, before time.Time) error {
	if vhost == "" {
		return nil
	}
	return s.execWithRetry(ctx, func() error {
		_, err := s.exec(ctx, "DELETE FROM audit_logs WHERE vhost = ? AND timestamp < ?", vhost, before)
		return err
	})
}

// PurgeWorkflowVersions keeps a workflow's newest keep versions.
//
// The cut is read first and the delete is by version number, because the
// dialects do not agree on LIMIT inside a DELETE or a subquery.
func (s *sqlStorage) PurgeWorkflowVersions(ctx context.Context, workflowID string, keep int) error {
	if keep <= 0 {
		return nil
	}
	oldestKept, more, err := s.oldestKeptVersion(ctx, workflowID, keep)
	if err != nil || !more {
		return err
	}
	return s.execWithRetry(ctx, func() error {
		_, err := s.exec(ctx, "DELETE FROM workflow_versions WHERE workflow_id = ? AND version < ?", workflowID, oldestKept)
		return err
	})
}

// oldestKeptVersion returns the lowest of a workflow's newest keep versions,
// and whether the workflow has any older than it.
func (s *sqlStorage) oldestKeptVersion(ctx context.Context, workflowID string, keep int) (oldestKept int, more bool, err error) {
	rows, err := s.query(ctx, "SELECT version FROM workflow_versions WHERE workflow_id = ? ORDER BY version DESC", workflowID)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = rows.Close() }()
	seen := 0
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return 0, false, err
		}
		seen++
		if seen == keep {
			oldestKept = v
		}
	}
	return oldestKept, seen > keep, rows.Err()
}

// PurgeDecidedApprovals deletes a workflow's approvals decided before the time
// given. A pending approval has no processed_at and is what a suspended
// message is waiting on, so it never matches.
func (s *sqlStorage) PurgeDecidedApprovals(ctx context.Context, workflowID string, before time.Time) error {
	return s.execWithRetry(ctx, func() error {
		_, err := s.exec(ctx, "DELETE FROM approvals WHERE workflow_id = ? AND status <> 'pending' AND status <> '' "+
			"AND processed_at IS NOT NULL AND processed_at < ?", workflowID, before)
		return err
	})
}

// backfillAuditVHosts gives audit rows written before the vhost column existed
// the vhost of the workflow, source or sink they are about, as it is now.
// Without it a vhost's retention would reach only rows written after the
// upgrade, and the rows a deployment already has too many of would stay.
//
// It is idempotent and runs on every start: once a row has a vhost it no
// longer matches. A row about something deleted, or about a user, matches
// nothing and stays in no vhost. Best-effort, like the indexes: the log may be
// kept in a database that does not hold the workflows, where there is nothing
// to read a vhost from.
func (s *sqlStorage) backfillAuditVHosts(ctx context.Context) {
	for entityType, table := range map[string]string{"workflow": "workflows", "source": "sources", "sink": "sinks"} {
		query := "UPDATE audit_logs SET vhost = (SELECT CASE WHEN e.vhost IS NULL OR e.vhost = '' OR e.vhost = 'all' THEN 'default' ELSE e.vhost END " +
			"FROM " + table + " e WHERE e.id = audit_logs.entity_id) " +
			"WHERE (vhost IS NULL OR vhost = '') AND entity_type = ? " +
			"AND EXISTS (SELECT 1 FROM " + table + " e WHERE e.id = audit_logs.entity_id)"
		_, _ = s.db.ExecContext(ctx, s.prepareQuery(query), entityType) //nolint:gosec // table names are the three literals above; the value is bound.
	}
}

// prepareVHostRetention adds what the per-vhost retention sweep needs to a
// database of any age. It runs after autoMigrate, which is what adds
// audit_logs.vhost to a database made before the column existed.
func (s *sqlStorage) prepareVHostRetention(ctx context.Context) {
	for _, index := range []string{
		"CREATE INDEX IF NOT EXISTS idx_audit_vhost_ts ON audit_logs(vhost, timestamp)",
		"CREATE INDEX IF NOT EXISTS idx_approvals_workflow ON approvals(workflow_id, processed_at)",
	} {
		// Ignored, as for every other index: it may already exist.
		_, _ = s.db.ExecContext(ctx, s.prepareQuery(index)) //nolint:gosec // the two literals above.
	}
	s.backfillAuditVHosts(ctx)
}
