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
	"slices"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/environment"
	"github.com/gsoultan/hermod/internal/storage"
)

func TestTheApproverFillsInTheSecretsAPromotionNeedsOverHTTP(t *testing.T) {
	stg, prd, link := linked(t)
	ctx := t.Context()
	const stgValue, prdValue = "stg-crm-key-77a1", "prd-crm-key-19c4"
	if err := stg.st.(storage.VHostSecretStore).PutVHostSecret(ctx, storage.VHostSecret{VHost: "default", Name: "CRM_KEY", Value: stgValue, UpdatedBy: "ada"}); err != nil {
		t.Fatal(err)
	}
	wf, err := stg.st.GetWorkflow(ctx, "wf-orders")
	if err != nil {
		t.Fatal(err)
	}
	wf.Nodes[1].Config = map[string]any{"api_key": "{{secret.CRM_KEY}}"}
	if err := stg.st.UpdateWorkflow(ctx, wf); err != nil {
		t.Fatal(err)
	}

	var pv environment.Preview
	stg.must(t, http.StatusOK, "editor", http.MethodPost, "/api/environments/links/"+link.ID+"/preview",
		map[string]any{"workflow_id": "wf-orders"}, &pv)
	if !slices.Equal(pv.MissingSecrets, []string{"CRM_KEY"}) {
		t.Fatalf("missing secrets = %v, want [CRM_KEY]", pv.MissingSecrets)
	}

	var sent environment.Promotion
	stg.must(t, http.StatusCreated, "editor", http.MethodPost, "/api/environments/links/"+link.ID+"/promotions",
		map[string]any{"workflow_id": "wf-orders"}, &sent)
	code, body := prd.call(t, "admin", http.MethodGet, "/api/environments/promotions", nil)
	if code != http.StatusOK || !strings.Contains(string(body), `"secrets":["CRM_KEY"]`) {
		t.Fatalf("listing promotions: %d %s", code, body)
	}
	if strings.Contains(string(body), stgValue) {
		t.Fatal("staging's value reached production")
	}

	var done environment.Promotion
	prd.must(t, http.StatusOK, "admin", http.MethodPost, "/api/environments/promotions/"+sent.ID+"/approve",
		map[string]any{"secrets": map[string]string{"CRM_KEY": prdValue}}, &done)
	if !slices.Equal(done.SecretsCopied, []string{"CRM_KEY"}) {
		t.Fatalf("approved = %+v", done)
	}
	got, err := prd.st.(storage.VHostSecretStore).GetVHostSecret(ctx, "default", "CRM_KEY")
	if err != nil || got.Value != prdValue || got.UpdatedBy != "root" {
		t.Fatalf("production's CRM_KEY = %+v, %v", got, err)
	}
}

func TestTheSenderTypesProductionsSecretValueOverHTTP(t *testing.T) {
	stg, prd, link := linked(t)
	ctx := t.Context()
	wf, err := stg.st.GetWorkflow(ctx, "wf-orders")
	if err != nil {
		t.Fatal(err)
	}
	wf.Nodes[1].Config = map[string]any{"api_key": "{{secret.CRM_KEY}}"}
	if err := stg.st.UpdateWorkflow(ctx, wf); err != nil {
		t.Fatal(err)
	}
	const typed = "prd-typed-on-staging-5e2"

	var sent environment.Promotion
	stg.must(t, http.StatusCreated, "editor", http.MethodPost, "/api/environments/links/"+link.ID+"/promotions",
		map[string]any{"workflow_id": "wf-orders", "secrets": map[string]string{"CRM_KEY": typed}}, &sent)
	if !slices.Equal(sent.SecretsFilled, []string{"CRM_KEY"}) {
		t.Fatalf("sent = %+v", sent)
	}
	for _, in := range []*instance{stg, prd} {
		code, body := in.call(t, "admin", http.MethodGet, "/api/environments/promotions", nil)
		if code != http.StatusOK || strings.Contains(string(body), typed) {
			t.Fatalf("a promotion listing shows the value: %d %s", code, body)
		}
	}
	if _, err := stg.st.(storage.VHostSecretStore).GetVHostSecret(ctx, "default", "CRM_KEY"); err == nil {
		t.Fatal("staging stored production's value")
	}

	var done environment.Promotion
	prd.must(t, http.StatusOK, "admin", http.MethodPost, "/api/environments/promotions/"+sent.ID+"/approve", nil, &done)
	got, err := prd.st.(storage.VHostSecretStore).GetVHostSecret(ctx, "default", "CRM_KEY")
	if err != nil || got.Value != typed {
		t.Fatalf("production's CRM_KEY = %+v, %v", got, err)
	}
}
