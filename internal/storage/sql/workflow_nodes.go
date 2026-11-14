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
	"log"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/storage/nodeclean"
)

// workflowVHost is the vhost a workflow is saved in, "default" when the
// workflow cannot be read. A version row has no vhost of its own, and the
// credentials moved out of it belong to its workflow's vhost.
func (s *sqlStorage) workflowVHost(ctx context.Context, id string) string {
	wf, err := s.GetWorkflow(ctx, id)
	if err != nil || wf.VHost == "" {
		return "default"
	}
	return wf.VHost
}

// storedNodes is one row's nodes column, as written.
type storedNodes struct {
	id, workflowID, vhost, nodes string
}

// CleanStoredWorkflows rewrites every workflow and version whose saved nodes
// hold what nodeclean removes: copies of a connection (credentials
// included), captured sample rows, and credentials typed into a step, which
// move into the workflow's vhost secrets. Reads are cleaned anyway, so this
// is about the bytes at rest and in every backup taken from now on.
//
// It runs at start-up and is idempotent: a clean row is not written. Versions
// go first, so where an old version and the workflow hold different values
// for one step's credential, the secret ends up with the workflow's current
// one. It returns how many rows it rewrote.
func (s *sqlStorage) CleanStoredWorkflows(ctx context.Context) (int, error) {
	workflows, err := s.readNodeRows(ctx, false)
	if err != nil {
		return 0, err
	}
	vhostOf := make(map[string]string, len(workflows))
	for _, r := range workflows {
		vhostOf[r.id] = r.vhost
	}
	versions, err := s.readNodeRows(ctx, true)
	if err != nil {
		return 0, err
	}
	rewritten := 0
	for _, batch := range []struct {
		rows     []storedNodes
		versions bool
	}{{versions, true}, {workflows, false}} {
		for _, r := range batch.rows {
			next, changed, err := cleanNodesJSON(ctx, s, vhostOf[r.workflowID], r.workflowID, r.nodes)
			if err != nil {
				return rewritten, fmt.Errorf("cleaning nodes of %s: %w", r.id, err)
			}
			if !changed {
				continue
			}
			if err := s.writeNodes(ctx, batch.versions, r.id, next); err != nil {
				return rewritten, fmt.Errorf("rewriting nodes of %s: %w", r.id, err)
			}
			rewritten++
		}
	}
	return rewritten, nil
}

// readNodeRows reads the nodes column of every workflow, or of every version.
// The two statements are literals, never caller input.
func (s *sqlStorage) readNodeRows(ctx context.Context, versions bool) ([]storedNodes, error) {
	var rows *sql.Rows
	var err error
	if versions {
		rows, err = s.db.QueryContext(ctx, s.prepareQuery("SELECT id, workflow_id, '', nodes FROM workflow_versions"))
	} else {
		rows, err = s.db.QueryContext(ctx, s.prepareQuery("SELECT id, id, vhost, nodes FROM workflows"))
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []storedNodes
	for rows.Next() {
		var r storedNodes
		var vhost, nodes sql.NullString
		if err := rows.Scan(&r.id, &r.workflowID, &vhost, &nodes); err != nil {
			return nil, err
		}
		r.vhost, r.nodes = vhost.String, nodes.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// writeNodes replaces one workflow's or version's nodes column.
func (s *sqlStorage) writeNodes(ctx context.Context, versions bool, id, nodes string) error {
	var err error
	if versions {
		_, err = s.db.ExecContext(ctx, s.prepareQuery("UPDATE workflow_versions SET nodes = ? WHERE id = ?"), nodes, id)
	} else {
		_, err = s.db.ExecContext(ctx, s.prepareQuery("UPDATE workflows SET nodes = ? WHERE id = ?"), nodes, id)
	}
	return err
}

// cleanNodesJSON returns the nodes column as ForWrite would write it, and
// whether that differs from what is stored. A column that is not a node
// list is left alone.
func cleanNodesJSON(ctx context.Context, store nodeclean.SecretStore, vhost, workflowID, raw string) (string, bool, error) {
	if raw == "" {
		return raw, false, nil
	}
	var nodes []storage.WorkflowNode
	if err := json.Unmarshal([]byte(raw), &nodes); err != nil {
		log.Printf("storage: workflow %s has nodes that are not a node list; left as they are: %v", workflowID, err)
		return raw, false, nil
	}
	before, _ := json.Marshal(nodes)
	cleaned, err := nodeclean.ForWrite(ctx, store, vhost, workflowID, nodes)
	if err != nil {
		return raw, false, err
	}
	after, err := json.Marshal(cleaned)
	if err != nil {
		return raw, false, err
	}
	return string(after), string(after) != string(before), nil
}
