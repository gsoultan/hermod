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
)

// warehouseNeeds gives production's warehouse sink a password read from a
// vhost secret, the way most connections keep their credentials.
func warehouseNeeds(p *pair, name string) {
	snk := p.prdStore.sinks["p-wh"]
	snk.Config = map[string]string{"user": "loader", "password": "{{secret." + name + "}}"}
	p.prdStore.sinks["p-wh"] = snk
}

func TestPreviewAsksForTheSecretsOfTheConnectionsTheWorkflowUses(t *testing.T) {
	p, l, _ := vaulted(t)
	warehouseNeeds(p, "WH_PASSWORD")
	pv, err := p.stg.Preview(context.Background(), l.ID, "wf-orders", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pv.MissingSecrets, []string{"CRM_KEY", "WH_PASSWORD"}) {
		t.Fatalf("missing secrets = %v, want [CRM_KEY WH_PASSWORD]", pv.MissingSecrets)
	}
	if got := pv.SecretUses["WH_PASSWORD"]; !slices.Equal(got, []string{"sink warehouse"}) {
		t.Fatalf("WH_PASSWORD is used by %v, want [sink warehouse]", got)
	}
}

func TestApprovingFillsInTheSecretsOfTheConnectionsTheWorkflowUses(t *testing.T) {
	ctx := context.Background()
	p, l, vault := vaulted(t)
	warehouseNeeds(p, "WH_PASSWORD")
	sent := sendOrders(t, p, l)
	if !slices.Equal(sent.Secrets, []string{"CRM_KEY", "WH_PASSWORD"}) {
		t.Fatalf("promotion secrets = %v, want [CRM_KEY WH_PASSWORD]", sent.Secrets)
	}
	got, err := p.prd.ApproveWith(ctx, sent.ID, "rina", map[string]string{"WH_PASSWORD": "s3cret"})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !slices.Equal(got.SecretsCopied, []string{"WH_PASSWORD"}) || !slices.Equal(got.SecretsMissing, []string{"CRM_KEY"}) {
		t.Fatalf("copied %v, missing %v", got.SecretsCopied, got.SecretsMissing)
	}
	if v := vault.values["default/WH_PASSWORD"]; v != "s3cret" {
		t.Fatalf("WH_PASSWORD = %q", v)
	}
}

func TestTheSenderCanTypeTheSecretOfAConnection(t *testing.T) {
	ctx := context.Background()
	p, l, _ := vaulted(t)
	warehouseNeeds(p, "WH_PASSWORD")
	sent, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{Start: true, SecretValues: map[string]string{"WH_PASSWORD": "s3cret"}})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	got, err := p.prd.promotion(ctx, sent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.SecretsFilled, []string{"WH_PASSWORD"}) {
		t.Fatalf("filled = %v", got.SecretsFilled)
	}
}
