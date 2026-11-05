// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"net/http"
	"testing"

	"github.com/gsoultan/hermod/internal/environment"
	"github.com/gsoultan/hermod/internal/storage"
)

// A sink production lacks travels with the promotion once its plain token is
// moved to a secret, and approving creates it there with production's value.
func TestASinkProductionLacksIsCarriedOverHTTP(t *testing.T) {
	stg, prd, link := linked(t)
	ctx := t.Context()
	if err := stg.st.CreateSink(ctx, storage.Sink{ID: "k-slack", Name: "slack-alerts", Type: "slack", VHost: "default",
		Config: map[string]string{"channel": "#orders", "token": "xoxb-staging"}}); err != nil {
		t.Fatal(err)
	}
	wf, err := stg.st.GetWorkflow(ctx, "wf-orders")
	if err != nil {
		t.Fatal(err)
	}
	wf.Nodes = append(wf.Nodes, storage.WorkflowNode{ID: "n3", Type: "sink", RefID: "k-slack"})
	wf.Edges = append(wf.Edges, storage.WorkflowEdge{ID: "e2", SourceID: "n1", TargetID: "n3"})
	if err := stg.st.UpdateWorkflow(ctx, wf); err != nil {
		t.Fatal(err)
	}

	var pv environment.Preview
	stg.must(t, http.StatusOK, "editor", http.MethodPost, "/api/environments/links/"+link.ID+"/preview", map[string]any{"workflow_id": "wf-orders"}, &pv)
	if pv.Ready {
		t.Fatal("ready while the token is plain text")
	}

	stg.must(t, http.StatusNoContent, "editor", http.MethodPost, "/api/environments/connections/sink/k-slack/secret",
		map[string]string{"key": "token", "name": "SLACK_TOKEN"}, nil)
	stg.must(t, http.StatusOK, "editor", http.MethodPost, "/api/environments/links/"+link.ID+"/preview", map[string]any{"workflow_id": "wf-orders"}, &pv)
	if !pv.Ready {
		t.Fatalf("not ready: %+v", pv.Checks)
	}

	var sent environment.Promotion
	stg.must(t, http.StatusCreated, "editor", http.MethodPost, "/api/environments/links/"+link.ID+"/promotions", map[string]any{
		"workflow_id": "wf-orders", "secrets": map[string]string{"SLACK_TOKEN": "xoxb-prod"},
		"settings": map[string]map[string]string{"k-slack": {"channel": "#orders-prod"}},
	}, &sent)
	prd.must(t, http.StatusOK, "admin", http.MethodPost, "/api/environments/promotions/"+sent.ID+"/approve", nil, nil)

	snk, err := prd.st.GetSink(ctx, "k-slack")
	if err != nil {
		t.Fatalf("sink not created in production: %v", err)
	}
	if snk.Config["channel"] != "#orders-prod" || snk.Config["token"] != "{{secret.SLACK_TOKEN}}" {
		t.Fatalf("sink config = %v", snk.Config)
	}
	got, err := prd.st.(storage.VHostSecretStore).GetVHostSecret(ctx, "default", "SLACK_TOKEN")
	if err != nil || got.Value != "xoxb-prod" {
		t.Fatalf("production's SLACK_TOKEN = %+v, %v", got, err)
	}
}
