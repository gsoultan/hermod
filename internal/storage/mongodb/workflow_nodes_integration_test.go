// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

//go:build integration
// +build integration

package mongodb

import (
	"encoding/json"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/gsoultan/hermod/internal/storage"
)

// The SQL contract (internal/storage/sql/workflow_nodes_test.go) on MongoDB:
// a saved workflow and its version hold no connection copy and no typed step
// credential, and a document written before the rule is rewritten by the sweep.
func TestMongoSavedWorkflowsHoldNoCredentials(t *testing.T) {
	s, db := newTraceMongo(t)
	ms := s.(*mongoStorage)
	ctx := t.Context()
	nodes := []storage.WorkflowNode{
		{ID: "n1", Type: "source", RefID: "src-1", Config: map[string]any{
			"label": "pg-src", "config": map[string]any{"password": "S3cretPW-src"}}},
		{ID: "n2", Type: "transformation", Config: map[string]any{
			"label": "lookup", "transType": "api_lookup", "password": "S3cretPW-step"}},
	}
	wf := storage.Workflow{ID: "wf-mongo-1", Name: "probe", VHost: "default", Nodes: nodes}
	if err := ms.CreateWorkflow(ctx, wf); err != nil {
		t.Fatal(err)
	}
	if err := ms.CreateWorkflowVersion(ctx, storage.WorkflowVersion{ID: "v1", WorkflowID: wf.ID, Version: 1, Nodes: nodes}); err != nil {
		t.Fatal(err)
	}
	// Written as an older release would have.
	if _, err := db.Collection("workflows").InsertOne(ctx, bson.M{"_id": "wf-old", "vhost": "default", "nodes": nodes}); err != nil {
		t.Fatal(err)
	}
	if _, err := ms.CleanStoredWorkflows(ctx); err != nil {
		t.Fatal(err)
	}
	for _, coll := range []string{"workflows", "workflow_versions"} {
		cur, err := db.Collection(coll).Find(ctx, bson.M{})
		if err != nil {
			t.Fatal(err)
		}
		var docs []bson.M
		if err := cur.All(ctx, &docs); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(docs)
		if strings.Contains(string(raw), "S3cretPW") {
			t.Errorf("%s still holds a credential: %s", coll, raw)
		}
	}
}
