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

func TestAFlowApprovedWithAnEmptySecretIsKeptStopped(t *testing.T) {
	ctx := context.Background()
	p, l, _ := vaulted(t)
	sent := sendOrders(t, p, l)

	got, err := p.prd.ApproveWith(ctx, sent.ID, "rina", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusDeployed || !got.KeptStopped || !slices.Equal(got.SecretsMissing, []string{"CRM_KEY"}) {
		t.Fatalf("promotion = %+v, want deployed and kept stopped", got)
	}
	wf, err := p.prdStore.GetWorkflow(ctx, "wf-orders")
	if err != nil || wf.Active {
		t.Fatalf("workflow active = %v, %v; want stopped until CRM_KEY has a value", wf.Active, err)
	}
}

func TestTheApproverCanStartAFlowWithAnEmptySecretAnyway(t *testing.T) {
	ctx := context.Background()
	p, l, _ := vaulted(t)
	sent := sendOrders(t, p, l)

	// A secret manager serves CRM_KEY, so nothing is typed for it.
	got, err := p.prd.ApproveWithOptions(ctx, sent.ID, "rina", ApproveOptions{StartAnyway: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusDeployed || got.KeptStopped {
		t.Fatalf("promotion = %+v, want deployed and started", got)
	}
	wf, err := p.prdStore.GetWorkflow(ctx, "wf-orders")
	if err != nil || !wf.Active {
		t.Fatalf("workflow active = %v, %v; want started", wf.Active, err)
	}
}

func TestAFlowWithEverySecretFilledStartsAsAsked(t *testing.T) {
	ctx := context.Background()
	p, l, _ := vaulted(t)
	sent := sendOrders(t, p, l)

	got, err := p.prd.ApproveWith(ctx, sent.ID, "rina", map[string]string{"CRM_KEY": "prod-crm-key"})
	if err != nil {
		t.Fatal(err)
	}
	if got.KeptStopped {
		t.Fatalf("promotion = %+v, want started", got)
	}
	if wf, _ := p.prdStore.GetWorkflow(ctx, "wf-orders"); !wf.Active {
		t.Fatal("the workflow was not started")
	}
}

func TestARunningFlowIsStoppedWhenItsNewVersionLacksASecret(t *testing.T) {
	ctx := context.Background()
	p, l, vault := vaulted(t)
	first := sendOrders(t, p, l)
	vault.values["default/CRM_KEY"] = "prod-crm-key"
	if _, err := p.prd.Approve(ctx, first.ID, "rina"); err != nil {
		t.Fatal(err)
	}
	if wf, _ := p.prdStore.GetWorkflow(ctx, "wf-orders"); !wf.Active {
		t.Fatal("the first version did not start")
	}

	// The secret goes away, and the next version is approved without it.
	delete(vault.values, "default/CRM_KEY")
	second := sendOrders(t, p, l)
	got, err := p.prd.ApproveWith(ctx, second.ID, "rina", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.KeptStopped {
		t.Fatalf("promotion = %+v, want kept stopped", got)
	}
	if wf, _ := p.prdStore.GetWorkflow(ctx, "wf-orders"); wf.Active {
		t.Fatal("the workflow kept running without CRM_KEY")
	}
}

func TestStagingCanApproveAndStartAFlowWithAnEmptySecret(t *testing.T) {
	ctx := context.Background()
	for _, startAnyway := range []bool{false, true} {
		p, l := decidable(t)
		p.prd.secrets = newVault()
		sent, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{Start: true})
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.stg.DecideSentWith(ctx, sent.ID, DecisionApprove, "rina", "", startAnyway)
		if err != nil {
			t.Fatalf("DecideSentWith: %v", err)
		}
		if got.KeptStopped == startAnyway {
			t.Fatalf("start anyway %v: kept stopped = %v", startAnyway, got.KeptStopped)
		}
		if wf, _ := p.prdStore.GetWorkflow(ctx, "wf-orders"); wf.Active != startAnyway {
			t.Fatalf("start anyway %v: production's workflow active = %v", startAnyway, wf.Active)
		}
	}
}
