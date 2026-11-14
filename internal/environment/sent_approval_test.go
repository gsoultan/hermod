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
	"testing"
)

// The sending side has to know whether the person who sent a release may
// approve it from there. It used to offer them Approve, count the release in
// their Approvals badge, and only then pass on production's refusal. The sent
// log now keeps the rule production gave when the release was checked.
func TestTheSentLogKeepsWhoMayApprove(t *testing.T) {
	ctx := context.Background()
	p, l := decidable(t)
	one, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if one.Approval != ApproveAnyAdminExceptSender {
		t.Fatalf("a promotion records approval %q, want %q", one.Approval, ApproveAnyAdminExceptSender)
	}
	if _, err := p.prd.Reject(ctx, one.ID, "rina", "next one"); err != nil {
		t.Fatal(err)
	}

	if _, err := p.prd.SetRules(ctx, Rules{Approval: ApproveAnyAdmin, AutoRollback: true}); err != nil {
		t.Fatal(err)
	}
	rel, err := p.stg.SendRelease(ctx, []ReleaseItem{{WorkflowID: "wf-orders"}}, "gembit", PromoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := rel[0].Promotions[0].Approval; got != ApproveAnyAdmin {
		t.Fatalf("a release records approval %q, want %q", got, ApproveAnyAdmin)
	}

	log, err := p.stg.Sent(ctx)
	if err != nil || len(log) != 2 {
		t.Fatalf("sent log = %+v, %v", log, err)
	}
	if log[0].Approval != ApproveAnyAdmin || log[1].Approval != ApproveAnyAdminExceptSender {
		t.Fatalf("the sent log lost the rules: %q, %q", log[0].Approval, log[1].Approval)
	}
}
