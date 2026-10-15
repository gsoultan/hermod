// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package lineage

import (
	"errors"
	"slices"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
)

// chain: pg.orders -> w1 -> kafka.orders -> w2 -> es.orders
//
//	\-> w3 -> s3 (w3 also looks up pg.countries)
func chain() Graph {
	return Derive(Inputs{
		Sources: []storage.Source{
			{ID: "pg", Name: "orders db", Type: "postgres", Config: hermod.StringMap{"host": "db", "port": "5432", "dbname": "shop", "tables": "orders"}},
			{ID: "k", Name: "orders topic", Type: "kafka", Config: hermod.StringMap{"brokers": "b:9092", "topic": "orders"}},
			{ID: "ref", Name: "ref db", Type: "postgres", Config: hermod.StringMap{"host": "db", "port": "5432", "dbname": "shop"}},
		},
		Sinks: []storage.Sink{
			{ID: "kout", Name: "orders out", Type: "kafka", Config: hermod.StringMap{"brokers": "b:9092", "topic": "orders"}},
			{ID: "es", Name: "search", Type: "elasticsearch", Config: hermod.StringMap{"addresses": "http://es:9200", "index": "orders"}},
			{ID: "s3", Name: "archive", Type: "s3", Config: hermod.StringMap{"bucket": "lake", "key_prefix": "orders/"}},
		},
		Workflows: []storage.Workflow{
			{ID: "w1", Name: "capture", Nodes: []storage.WorkflowNode{{ID: "a", Type: "source", RefID: "pg"}, {ID: "b", Type: "sink", RefID: "kout"}}},
			{ID: "w2", Name: "index", Nodes: []storage.WorkflowNode{{ID: "a", Type: "source", RefID: "k"}, {ID: "b", Type: "sink", RefID: "es"}}},
			{ID: "w3", Name: "archive", Nodes: []storage.WorkflowNode{
				{ID: "a", Type: "source", RefID: "k"},
				{ID: "l", Type: "transformation", Config: map[string]any{"transType": "db_lookup", "sourceId": "ref", "table": "countries"}},
				{ID: "b", Type: "sink", RefID: "s3"},
			}},
		},
	})
}

func TestDownstreamIsTransitiveWithThePath(t *testing.T) {
	g := chain()
	imp, err := Downstream(g, "table:postgres://db:5432/shop/orders")
	if err != nil {
		t.Fatal(err)
	}
	if imp.Root.ID != "table:postgres://db:5432/shop/orders" {
		t.Fatalf("root = %+v", imp.Root)
	}
	got := map[string][]string{}
	var order []string
	for _, it := range imp.Impacted {
		got[it.Node.ID] = it.Path
		order = append(order, it.Node.ID)
	}
	want := map[string][]string{
		"workflow:w1":                          {"table:postgres://db:5432/shop/orders", "workflow:w1"},
		"topic:kafka://b:9092/orders":          {"table:postgres://db:5432/shop/orders", "workflow:w1", "topic:kafka://b:9092/orders"},
		"workflow:w2":                          {"table:postgres://db:5432/shop/orders", "workflow:w1", "topic:kafka://b:9092/orders", "workflow:w2"},
		"workflow:w3":                          {"table:postgres://db:5432/shop/orders", "workflow:w1", "topic:kafka://b:9092/orders", "workflow:w3"},
		"index:elasticsearch://es:9200/orders": {"table:postgres://db:5432/shop/orders", "workflow:w1", "topic:kafka://b:9092/orders", "workflow:w2", "index:elasticsearch://es:9200/orders"},
		"object:s3://lake/orders/":             {"table:postgres://db:5432/shop/orders", "workflow:w1", "topic:kafka://b:9092/orders", "workflow:w3", "object:s3://lake/orders/"},
	}
	if len(got) != len(want) {
		t.Fatalf("impacted = %v, want %d nodes", order, len(want))
	}
	for id, path := range want {
		if !slices.Equal(got[id], path) {
			t.Errorf("path to %s = %v, want %v", id, got[id], path)
		}
	}
	// Nearest first, so the list reads as the order things break in.
	for i := 1; i < len(imp.Impacted); i++ {
		if imp.Impacted[i].Depth < imp.Impacted[i-1].Depth {
			t.Fatalf("not ordered by depth: %v", order)
		}
	}
	if imp.Workflows != 3 || imp.Assets != 3 {
		t.Errorf("counts = %d workflows, %d assets", imp.Workflows, imp.Assets)
	}
	// The lookup table is upstream of w3, not downstream of the orders table.
	if _, ok := got["table:postgres://db:5432/shop/countries"]; ok {
		t.Errorf("an upstream lookup table is reported as impacted")
	}
}

func TestDownstreamOfALookupTableReachesTheWorkflowThatReadsIt(t *testing.T) {
	imp, err := Downstream(chain(), "table:postgres://db:5432/shop/countries")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, it := range imp.Impacted {
		ids = append(ids, it.Node.ID)
	}
	want := []string{"workflow:w3", "object:s3://lake/orders/"}
	if !slices.Equal(ids, want) {
		t.Fatalf("impacted = %v, want %v", ids, want)
	}
	if imp.Impacted[0].Relation != RelLooksUp {
		t.Errorf("relation into w3 = %q", imp.Impacted[0].Relation)
	}
}

func TestDownstreamOfAWorkflow(t *testing.T) {
	imp, err := Downstream(chain(), WorkflowNodeID("w2"))
	if err != nil {
		t.Fatal(err)
	}
	if len(imp.Impacted) != 1 || imp.Impacted[0].Node.ID != "index:elasticsearch://es:9200/orders" {
		t.Fatalf("impacted = %+v", imp.Impacted)
	}
}

func TestDownstreamSurvivesACycle(t *testing.T) {
	// w reads the topic and writes it back: a loop through one asset.
	g := Derive(Inputs{
		Sources: []storage.Source{{ID: "s", Type: "kafka", Config: hermod.StringMap{"brokers": "b", "topic": "t"}}},
		Sinks:   []storage.Sink{{ID: "k", Type: "kafka", Config: hermod.StringMap{"brokers": "b", "topic": "t"}}},
		Workflows: []storage.Workflow{{ID: "w", Nodes: []storage.WorkflowNode{
			{ID: "a", Type: "source", RefID: "s"}, {ID: "b", Type: "sink", RefID: "k"},
		}}},
	})
	imp, err := Downstream(g, "topic:kafka://b/t")
	if err != nil {
		t.Fatal(err)
	}
	if len(imp.Impacted) != 1 || imp.Impacted[0].Node.ID != "workflow:w" {
		t.Fatalf("impacted = %+v", imp.Impacted)
	}
}

func TestDownstreamOfAnUnknownNode(t *testing.T) {
	if _, err := Downstream(chain(), "table:nowhere"); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("err = %v, want ErrUnknownNode", err)
	}
}
