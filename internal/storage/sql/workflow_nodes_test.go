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
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// The editor's own save, as captured from a dev instance: a source node with
// the whole source spread over it and a lookup step with a typed password.
func editorSavedWorkflow() storage.Workflow {
	return storage.Workflow{
		ID: "21e197ed-efe4-4679-97ed-a7c4f12dfea3", Name: "probe", VHost: "default",
		Nodes: []storage.WorkflowNode{
			{ID: "n1", Type: "source", RefID: "src-1", Config: map[string]any{
				"label": "pg-src", "type": "postgres", "ref_id": "src-1", "id": "src-1", "name": "pg-src",
				"active": true, "vhost": "default", "worker_id": "",
				"config":     map[string]any{"host": "db.internal", "user": "svc_reader", "password": "S3cretPW-src"},
				"lastSample": map[string]any{"email": "someone@example.com"},
			}},
			{ID: "n2", Type: "transformation", Config: map[string]any{
				"label": "lookup", "transType": "api_lookup", "url": "https://api.example.com/u",
				"targetField": "profile", "authType": "basic", "username": "apiuser", "password": "S3cretPW-step",
			}},
		},
	}
}

func assertNoCredentials(t *testing.T, where, raw string) {
	t.Helper()
	for _, leak := range []string{"S3cretPW", "svc_reader", "apiuser", "someone@example.com", "db.internal"} {
		if strings.Contains(raw, leak) {
			t.Errorf("%s still holds %q: %s", where, leak, raw)
		}
	}
}

func TestASavedWorkflowHoldsNoConnectionCopyOrTypedCredential(t *testing.T) {
	s := sampleStore(t).(*sqlStorage)
	ctx := t.Context()
	wf := editorSavedWorkflow()
	if err := s.CreateWorkflow(ctx, wf); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateWorkflow(ctx, wf); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateWorkflowVersion(ctx, storage.WorkflowVersion{ID: "v1", WorkflowID: wf.ID, Version: 1, Nodes: wf.Nodes}); err != nil {
		t.Fatal(err)
	}

	var row, version string
	if err := s.db.QueryRowContext(ctx, "SELECT nodes FROM workflows WHERE id = ?", wf.ID).Scan(&row); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT nodes FROM workflow_versions WHERE id = 'v1'").Scan(&version); err != nil {
		t.Fatal(err)
	}
	assertNoCredentials(t, "workflows.nodes", row)
	assertNoCredentials(t, "workflow_versions.nodes", version)

	got, err := s.GetWorkflow(ctx, wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	step := got.Nodes[1].Config
	if step["password"] != "{{secret.WF_21E197ED_LOOKUP_PASSWORD}}" || step["username"] != "{{secret.WF_21E197ED_LOOKUP_USERNAME}}" {
		t.Fatalf("step credentials = %v / %v", step["username"], step["password"])
	}
	secret, err := s.GetVHostSecret(ctx, "default", "WF_21E197ED_LOOKUP_PASSWORD")
	if err != nil || secret.Value != "S3cretPW-step" {
		t.Fatalf("the step's password did not reach the vhost's secrets: %v %v", secret.Value, err)
	}
	if got.Nodes[0].Config["label"] != "pg-src" || got.Nodes[0].RefID != "src-1" {
		t.Fatalf("the source node lost what it needs: %+v", got.Nodes[0])
	}
}

// Rows written before the rule: read back clean at once, rewritten at start-up.
func TestWorkflowsSavedBeforeTheRuleAreCleaned(t *testing.T) {
	s := sampleStore(t).(*sqlStorage)
	ctx := t.Context()
	wf := editorSavedWorkflow()
	raw := `[{"id":"n1","type":"source","ref_id":"src-1","config":{"label":"pg-src","config":{"password":"S3cretPW-src","user":"svc_reader","host":"db.internal"},"lastSample":{"e":"someone@example.com"}},"x":0,"y":0},` +
		`{"id":"n2","type":"transformation","config":{"label":"lookup","transType":"api_lookup","authType":"basic","username":"apiuser","password":"S3cretPW-step"},"x":0,"y":0}]`
	if err := s.CreateWorkflow(ctx, storage.Workflow{ID: wf.ID, Name: "old", VHost: "default"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE workflows SET nodes = ? WHERE id = ?", raw, wf.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO workflow_versions (id, workflow_id, version, nodes, edges, config, created_at) VALUES ('v1', ?, 1, ?, '[]', '{}', CURRENT_TIMESTAMP)", wf.ID, raw); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetWorkflow(ctx, wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, has := got.Nodes[0].Config["config"]; has {
		t.Fatal("a read handed back the connection copy")
	}

	n, err := s.CleanStoredWorkflows(ctx)
	if err != nil || n != 2 {
		t.Fatalf("rewrote %d rows, err %v; want 2", n, err)
	}
	var row, version string
	_ = s.db.QueryRowContext(ctx, "SELECT nodes FROM workflows WHERE id = ?", wf.ID).Scan(&row)
	_ = s.db.QueryRowContext(ctx, "SELECT nodes FROM workflow_versions WHERE id = 'v1'").Scan(&version)
	assertNoCredentials(t, "workflows.nodes", row)
	assertNoCredentials(t, "workflow_versions.nodes", version)
	if n, _ := s.CleanStoredWorkflows(ctx); n != 0 {
		t.Fatalf("a second run rewrote %d rows", n)
	}
}
