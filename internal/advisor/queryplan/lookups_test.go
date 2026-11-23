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
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
)

type fakeSources struct {
	sources map[string]storage.Source
	db      *sql.DB
	opened  atomic.Int32 // nodes are checked concurrently
}

func (f *fakeSources) GetSourceConfig(_ context.Context, id string) (storage.Source, error) {
	s, ok := f.sources[id]
	if !ok {
		return storage.Source{}, errors.New("source not found")
	}
	return s, nil
}

func (f *fakeSources) GetOrOpenDB(storage.Source) (*sql.DB, error) {
	f.opened.Add(1)
	if f.db == nil {
		return nil, errors.New("no database in this test")
	}
	return f.db, nil
}

func lookupNode(id, label, source, query string) storage.WorkflowNode {
	return storage.WorkflowNode{ID: id, Type: "transformation", Config: map[string]any{
		"transType": "db_lookup", "label": label, "sourceId": source, "mode": "query", "queryTemplate": query,
	}}
}

func TestCheckLookupsExplainsWhyANodeCannotBeChecked(t *testing.T) {
	srcs := &fakeSources{sources: map[string]storage.Source{
		"my":  {ID: "my", Name: "Shop", Type: "mysql", Config: hermod.StringMap{"use_cdc": "false"}},
		"cdc": {ID: "cdc", Name: "Live", Type: "postgres"},
		"pg":  {ID: "pg", Name: "ETL", Type: "postgres", Config: hermod.StringMap{"use_cdc": "false"}},
	}}
	wf := storage.Workflow{Nodes: []storage.WorkflowNode{
		{ID: "src", Type: "source"},
		{ID: "set", Type: "transformation", Config: map[string]any{"transType": "set"}},
		lookupNode("n1", "Orders", "my", "SELECT 1 FROM t WHERE a = {{x}}"),
		lookupNode("n2", "Live orders", "cdc", "SELECT 1"),
		lookupNode("n3", "Ghost", "gone", "SELECT 1"),
		lookupNode("n4", "Empty", "pg", ""),
	}}
	got := CheckLookups(t.Context(), wf, srcs)
	if len(got) != 4 {
		t.Fatalf("only db_lookup nodes are checked, got %+v", got)
	}
	want := map[string]string{"n1": "PostgreSQL", "n2": "non-CDC", "n3": "source not found", "n4": "no query"}
	for _, c := range got {
		if !strings.Contains(c.Error, want[c.NodeID]) {
			t.Fatalf("%s: error %q, want it to mention %q", c.NodeID, c.Error, want[c.NodeID])
		}
	}
	if got[0].NodeID != "n1" || got[0].Label != "Orders" || got[0].Source != "Shop" {
		t.Fatalf("results keep the flow's order and name the node and source: %+v", got[0])
	}
	if srcs.opened.Load() != 0 {
		t.Fatalf("no database is opened for a node that cannot be checked, opened %d", srcs.opened.Load())
	}
}
