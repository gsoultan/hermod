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

func TestPreviewNamesWhatToFixSoItCanBeFixedInPlace(t *testing.T) {
	ctx := context.Background()
	p, l := promotable(t)
	if _, err := p.stg.SetMapping(ctx, l.ID, map[string]string{"s-crm": ""}); err != nil {
		t.Fatal(err)
	}
	delete(p.secrets.envs, "REGION")

	pv, err := p.stg.Preview(ctx, l.ID, "wf-orders", 0)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Ready {
		t.Fatal("ready with an unmatched connection")
	}
	if len(pv.Unmatched) != 1 {
		t.Fatalf("unmatched = %+v", pv.Unmatched)
	}
	u := pv.Unmatched[0]
	if u.Kind != "source" || u.ID != "s-crm" || u.Name != "crm-stg" || u.Type != "mysql" {
		t.Fatalf("unmatched row = %+v", u)
	}
	// Production's connections are for administrators, through matching;
	// the preview is open to editors.
	if len(u.Candidates) != 0 || u.TargetID != "" || u.TargetName != "" {
		t.Fatalf("the preview leaked production's connections: %+v", u)
	}
	if len(pv.MissingEnvs) != 1 || pv.MissingEnvs[0] != "REGION" {
		t.Fatalf("missing envs = %v", pv.MissingEnvs)
	}

	// Matched and set, there is nothing left to fix.
	if _, err := p.stg.SetMapping(ctx, l.ID, map[string]string{"s-crm": "p-crm"}); err != nil {
		t.Fatal(err)
	}
	p.secrets.envs["REGION"] = true
	pv, err = p.stg.Preview(ctx, l.ID, "wf-orders", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !pv.Ready || len(pv.Unmatched) != 0 || len(pv.MissingEnvs) != 0 {
		t.Fatalf("preview = ready %v, unmatched %+v, envs %v", pv.Ready, pv.Unmatched, pv.MissingEnvs)
	}
}
