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
	"slices"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
)

// relationsOf lists "relation from->to" for every edge touching the workflow.
func relationsOf(g Graph) []string {
	var out []string
	for _, e := range g.Edges {
		out = append(out, e.Relation+" "+e.From+" -> "+e.To)
	}
	slices.Sort(out)
	return out
}

var lookupDB = storage.Source{ID: "ref", Type: "postgres", Config: hermod.StringMap{"host": "db", "port": "5432", "dbname": "ref", "use_cdc": "false"}}

// Each node is read with the config keys its transformer or executor reads:
// pkg/comm/transformer/lookup (db_lookup, api_lookup, reference_lookup),
// pkg/comm/transformer/ml (ml_predict, ml_train), genai/retrieve
// (ai_retrieve), advanced (execute_sql, scd), the agent's tools
// (internal/engine/registry/nodes/ai/agent/config.go) and pipeline steps
// (internal/engine/registry/nodes/core/transformation.go).
func TestDeriveNodeAssets(t *testing.T) {
	cases := []struct {
		name string
		node storage.WorkflowNode
		want []string
	}{
		{"db_lookup reads its table",
			storage.WorkflowNode{Type: "transformation", Config: map[string]any{"transType": "db_lookup", "sourceId": "ref", "table": "countries"}},
			[]string{"looks_up table:postgres://db:5432/ref/countries -> workflow:wf"}},
		{"db_lookup with the other spelling and a query template is the database",
			storage.WorkflowNode{Type: "transformation", Config: map[string]any{"transType": "db_lookup", "sourceID": "ref", "queryTemplate": "select 1"}},
			[]string{"looks_up database:postgres://db:5432/ref -> workflow:wf"}},
		{"db_lookup as its own node type",
			storage.WorkflowNode{Type: "db_lookup", Config: map[string]any{"sourceId": "ref", "table": "countries"}},
			[]string{"looks_up table:postgres://db:5432/ref/countries -> workflow:wf"}},
		{"db_lookup on a source outside the inputs adds nothing",
			storage.WorkflowNode{Type: "transformation", Config: map[string]any{"transType": "db_lookup", "sourceId": "other-vhost", "table": "t"}},
			nil},
		{"api_lookup reads an endpoint",
			storage.WorkflowNode{Type: "transformation", Config: map[string]any{"transType": "api_lookup", "url": "https://geo.example.com/v1/lookup?key=abc"}},
			[]string{"looks_up endpoint:http://geo.example.com/v1/lookup -> workflow:wf"}},
		{"redis_lookup reads a Redis database",
			storage.WorkflowNode{Type: "transformation", Config: map[string]any{"transType": "redis_lookup", "addr": "Cache:6379", "db": float64(2), "key": "user:{{.id}}"}},
			[]string{"looks_up database:redis://cache:6379/2 -> workflow:wf"}},
		{"redis_lookup with no db reads database 0",
			storage.WorkflowNode{Type: "transformation", Config: map[string]any{"transType": "redis_lookup", "addr": "cache:6379", "key": "k"}},
			[]string{"looks_up database:redis://cache:6379/0 -> workflow:wf"}},
		{"redis_lookup whose addr is a secret names no asset",
			storage.WorkflowNode{Type: "transformation", Config: map[string]any{"transType": "redis_lookup", "addr": "{{secret.REDIS_ADDR}}", "key": "k"}},
			nil},
		{"redis_set writes a Redis database",
			storage.WorkflowNode{Type: "transformation", Config: map[string]any{"transType": "redis_set", "addr": "cache:6379", "key": "k"}},
			[]string{"writes workflow:wf -> database:redis://cache:6379/0"}},
		{"reference_lookup reads a file",
			storage.WorkflowNode{Type: "transformation", Config: map[string]any{"transType": "reference_lookup", "filePath": "/ref/codes.csv"}},
			[]string{"looks_up file:file:///ref/codes.csv -> workflow:wf"}},
		{"ml_predict calls a model of the workflow's vhost",
			storage.WorkflowNode{Type: "transformation", Config: map[string]any{"transType": "ml_predict", "model": "churn"}},
			[]string{"predicts model:ml://tenant-a/churn -> workflow:wf"}},
		{"ml_train reads its dataset and writes its model",
			storage.WorkflowNode{Type: "transformation", Config: map[string]any{"transType": "ml_train", "model": "churn", "dataset": "events"}},
			[]string{"reads dataset:ml://tenant-a/events -> workflow:wf", "trains workflow:wf -> model:ml://tenant-a/churn"}},
		{"ml_train refilling its dataset from a database",
			storage.WorkflowNode{Type: "transformation", Config: map[string]any{"transType": "ml_train", "model": "churn", "dataset": "events", "sourceId": "ref", "query": "select 1"}},
			[]string{"reads database:postgres://db:5432/ref -> workflow:wf", "reads dataset:ml://tenant-a/events -> workflow:wf",
				"trains workflow:wf -> model:ml://tenant-a/churn", "writes workflow:wf -> dataset:ml://tenant-a/events"}},
		{"ai_retrieve on pgvector",
			storage.WorkflowNode{Type: "transformation", Config: map[string]any{"transType": "ai_retrieve", "store": "pgvector", "connectionString": "postgres://u:p@vec:5432/rag", "table": "docs"}},
			[]string{"retrieves table:postgres://vec:5432/rag/docs -> workflow:wf"}},
		{"ai_retrieve on pinecone",
			storage.WorkflowNode{Type: "transformation", Config: map[string]any{"transType": "ai_retrieve", "store": "pinecone", "indexHost": "https://idx-abc.svc.pinecone.io", "namespace": "faq", "storeApiKey": "k"}},
			[]string{"retrieves index:pinecone://idx-abc.svc.pinecone.io/faq -> workflow:wf"}},
		{"execute_sql writes into its source's database",
			storage.WorkflowNode{Type: "transformation", Config: map[string]any{"transType": "execute_sql", "sourceId": "ref", "queryTemplate": "update t set x=1"}},
			[]string{"writes workflow:wf -> database:postgres://db:5432/ref"}},
		{"scd writes its target and history tables",
			storage.WorkflowNode{Type: "transformation", Config: map[string]any{"transType": "scd", "targetSourceId": "ref", "targetTable": "dim_customer", "historyTable": "dim_customer_hist"}},
			[]string{"writes workflow:wf -> table:postgres://db:5432/ref/dim_customer", "writes workflow:wf -> table:postgres://db:5432/ref/dim_customer_hist"}},
		{"pipeline steps are read one by one",
			storage.WorkflowNode{Type: "transformation", Config: map[string]any{"transType": "pipeline",
				"steps": `[{"transType":"db_lookup","sourceId":"ref","table":"countries"},{"transType":"ml_predict","model":"churn"},{"transType":"mapping"}]`}},
			[]string{"looks_up table:postgres://db:5432/ref/countries -> workflow:wf", "predicts model:ml://tenant-a/churn -> workflow:wf"}},
		{"parallel pipeline steps given as a list",
			storage.WorkflowNode{Type: "transformation", Config: map[string]any{"transType": "parallel_pipeline",
				"steps": []any{map[string]any{"transType": "api_lookup", "url": "https://x.example.com/a"}}}},
			[]string{"looks_up endpoint:http://x.example.com/a -> workflow:wf"}},
		{"agent read tools",
			storage.WorkflowNode{Type: "ai_agent", Config: map[string]any{"goal": "g", "tools": `[
				{"name":"lookup","kind":"db_lookup","config":{"sourceId":"ref","table":"orders"}},
				{"name":"kb","kind":"ai_retrieve","config":{"store":"pinecone","indexHost":"idx.example.com","namespace":"kb"}},
				{"name":"score","kind":"ml_predict","config":{"model":"risk"}},
				{"name":"save","kind":"sink","nodeId":"n-sink"}]`}},
			[]string{"looks_up table:postgres://db:5432/ref/orders -> workflow:wf", "predicts model:ml://tenant-a/risk -> workflow:wf",
				"retrieves index:pinecone://idx.example.com/kb -> workflow:wf"}},
		{"a mapping node touches no asset",
			storage.WorkflowNode{Type: "transformation", Config: map[string]any{"transType": "mapping"}},
			nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.node.ID = "n1"
			g := Derive(Inputs{
				Sources:   []storage.Source{lookupDB},
				Workflows: []storage.Workflow{{ID: "wf", VHost: "tenant-a", Nodes: []storage.WorkflowNode{tc.node}}},
			})
			if got := relationsOf(g); !slices.Equal(got, tc.want) {
				t.Fatalf("edges =\n  %v\nwant\n  %v", got, tc.want)
			}
			for _, e := range g.Edges {
				if e.NodeID != "n1" || e.WorkflowID != "wf" {
					t.Errorf("edge %+v does not name its workflow node", e)
				}
			}
		})
	}
}

// A model Hermod retrains on a schedule reads its dataset
// (storage.MLModel.Retrain.Spec.Dataset), so the dataset feeds the model and,
// through it, every workflow that predicts with it.
func TestDeriveRetrainPolicyLinksDatasetToModel(t *testing.T) {
	in := Inputs{
		Models: []storage.MLModel{
			{VHost: "tenant-a", Name: "churn", Retrain: &storage.MLRetrainPolicy{Schedule: "@daily"}},
			{VHost: "tenant-a", Name: "plain"},
		},
	}
	in.Models[0].Retrain.Spec.Dataset = "events"
	g := Derive(in)
	want := []string{"trains dataset:ml://tenant-a/events -> model:ml://tenant-a/churn"}
	if got := relationsOf(g); !slices.Equal(got, want) {
		t.Fatalf("edges = %v, want %v", got, want)
	}
	if _, ok := g.Node("model:ml://tenant-a/plain"); !ok {
		t.Errorf("a registered model with no retraining is still an asset")
	}
}
