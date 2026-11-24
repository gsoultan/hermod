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
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/transformer/lookup"
)

// NodeTimeout bounds one node's check: opening its pool, planning, and
// reading the catalog.
const NodeTimeout = 15 * time.Second

// parallelChecks is how many nodes are checked at once. Each holds one
// connection of its source's pool for a few milliseconds.
const parallelChecks = 4

// Sources opens the databases lookups query. The engine registry is one.
type Sources interface {
	GetSourceConfig(ctx context.Context, id string) (storage.Source, error)
	GetOrOpenDB(src storage.Source) (*sql.DB, error)
}

// NodeCheck is one DB Lookup node's check.
type NodeCheck struct {
	NodeID string `json:"node_id"`
	Label  string `json:"label,omitempty"`
	// Source is the name of the source the node queries.
	Source string `json:"source,omitempty"`
	// SQL is the statement planned, with placeholders where message values go.
	SQL string `json:"sql,omitempty"`
	Check
	// Error says why the node could not be checked. SQL is still set when the
	// statement was built, so the reason can be read against it.
	Error string `json:"error,omitempty"`
}

// CheckLookups checks every DB Lookup node of wf, in the flow's order.
func CheckLookups(ctx context.Context, wf storage.Workflow, srcs Sources) []NodeCheck {
	var nodes []storage.WorkflowNode
	for _, n := range wf.Nodes {
		if tt, _ := n.Config["transType"].(string); n.Type == "transformation" && tt == "db_lookup" {
			nodes = append(nodes, n)
		}
	}
	out := make([]NodeCheck, len(nodes))
	sem := make(chan struct{}, parallelChecks)
	var wg sync.WaitGroup
	for i, n := range nodes {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = checkNode(ctx, n, srcs)
		})
	}
	wg.Wait()
	return out
}

func checkNode(ctx context.Context, n storage.WorkflowNode, srcs Sources) NodeCheck {
	ctx, cancel := context.WithTimeout(ctx, NodeTimeout)
	defer cancel()
	label, _ := n.Config["label"].(string)
	nc := NodeCheck{NodeID: n.ID, Label: label}
	fail := func(err error) NodeCheck {
		nc.Error = err.Error()
		return nc
	}
	src, driver, err := lookup.LookupTarget(ctx, srcs, n.Config)
	if err != nil {
		return fail(err)
	}
	nc.Source = src.Name
	if driver != "pgx" {
		return fail(errors.New("only PostgreSQL sources can be checked so far; this one is " + src.Type))
	}
	stmt, err := lookup.LookupStatement(n.Config, driver)
	if err != nil {
		return fail(err)
	}
	nc.SQL = stmt.SQL
	db, err := srcs.GetOrOpenDB(src)
	if err != nil {
		return fail(err)
	}
	if nc.Check, err = Run(ctx, db, stmt.SQL); err != nil {
		return fail(err)
	}
	return nc
}
