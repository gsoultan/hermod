// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package environment

import (
	"context"
	"slices"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// pricingStep is a step that reads a secret the way the value builder
// writes one: inside a function, in a header of a list.
func pricingStep() storage.WorkflowNode {
	return storage.WorkflowNode{ID: "n4", Type: "transformer", Config: map[string]any{
		"label": "Look up plan price",
		"headers": []any{
			map[string]any{"key": "Accept", "value": "application/json"},
			map[string]any{"key": "Authorization", "value": `{{concat("Bearer ", secret.PRICING_TOKEN)}}`},
		},
		"cacheKey": `secret("CACHE_SALT")`,
	}}
}

// withPricingStep saves ordersWorkflow with pricingStep as a new version.
func withPricingStep(p *pair) {
	wf := ordersWorkflow()
	wf.Nodes = append(wf.Nodes, pricingStep())
	p.stgStore.saveVersion(wf, "gembit", "pricing")
}

func TestReferencesFindsTheSecretsAStepReadsInsideAFunction(t *testing.T) {
	wf := storage.Workflow{Nodes: []storage.WorkflowNode{pricingStep()}}
	got, _ := references(wf)
	if want := []string{"CACHE_SALT", "PRICING_TOKEN"}; !slices.Equal(got, want) {
		t.Fatalf("references = %v, want %v", got, want)
	}
}

func TestPreviewAsksForTheSecretsAStepReadsInsideAFunction(t *testing.T) {
	p, l, _ := vaulted(t)
	withPricingStep(p)
	pv, err := p.stg.Preview(context.Background(), l.ID, "wf-orders", 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"CACHE_SALT", "CRM_KEY", "PRICING_TOKEN"}; !slices.Equal(pv.MissingSecrets, want) {
		t.Fatalf("missing secrets = %v, want %v", pv.MissingSecrets, want)
	}
}

func TestPreviewSaysWhereEachSecretIsUsed(t *testing.T) {
	p, l, _ := vaulted(t)
	warehouseNeeds(p, "WH_PASSWORD")
	withPricingStep(p)
	pv, err := p.stg.Preview(context.Background(), l.ID, "wf-orders", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]SecretPlace{
		"CACHE_SALT":    {{Kind: "step", Name: "Look up plan price", NodeID: "n4", Field: "Cache key", Value: `secret("CACHE_SALT")`}},
		"CRM_KEY":       {{Kind: "step", Name: "Lookup", NodeID: "n2", Field: "API key", Value: "{{secret.CRM_KEY}}"}},
		"PRICING_TOKEN": {{Kind: "step", Name: "Look up plan price", NodeID: "n4", Field: "Headers › Authorization", Value: `{{concat("Bearer ", secret.PRICING_TOKEN)}}`}},
		"WH_PASSWORD":   {{Kind: "sink", Name: "warehouse", Field: "Password"}},
	}
	for name, places := range want {
		if got := pv.SecretPlaces[name]; !slices.Equal(got, places) {
			t.Errorf("%s is used at %+v, want %+v", name, got, places)
		}
	}
	if len(pv.SecretPlaces) != len(want) {
		t.Errorf("places for %d secrets, want %d", len(pv.SecretPlaces), len(want))
	}
}

func TestAPromotionSaysWhereEachSecretIsUsed(t *testing.T) {
	p, l, _ := vaulted(t)
	warehouseNeeds(p, "WH_PASSWORD")
	withPricingStep(p)
	sent := sendOrders(t, p, l)
	if want := []string{"CACHE_SALT", "CRM_KEY", "PRICING_TOKEN", "WH_PASSWORD"}; !slices.Equal(sent.Secrets, want) {
		t.Fatalf("promotion secrets = %v, want %v", sent.Secrets, want)
	}
	if got := sent.SecretPlaces["PRICING_TOKEN"]; len(got) != 1 || got[0].Kind != "step" || got[0].Field != "Headers › Authorization" {
		t.Errorf("PRICING_TOKEN is used at %+v", got)
	}
	if got := sent.SecretPlaces["WH_PASSWORD"]; !slices.Equal(got, []SecretPlace{{Kind: "sink", Name: "warehouse", Field: "Password"}}) {
		t.Errorf("WH_PASSWORD is used at %+v", got)
	}
}

func TestFieldLabel(t *testing.T) {
	cases := map[string][]string{
		"API key":                     {"api_key"},
		"Password":                    {"password"},
		"MCP server URL":              {"mcpServerUrl"},
		"Headers › Authorization":     {"headers", "Authorization"},
		"Tools › 2 › Headers › X-Key": {"tools", "2", "headers", "X-Key"},
	}
	for want, path := range cases {
		if got := fieldLabel(path); got != want {
			t.Errorf("fieldLabel(%q) = %q, want %q", path, got, want)
		}
	}
}
