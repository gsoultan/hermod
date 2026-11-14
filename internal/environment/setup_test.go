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

// unsetPair is a staging and a production instance nobody has described.
func unsetPair(t *testing.T) *pair {
	t.Helper()
	p := newPair(t)
	ctx := context.Background()
	for _, s := range []*Service{p.stg, p.prd} {
		if _, err := s.SetInstance(ctx, Instance{}); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func TestConnectingSetsBothRolesWhenNobodyHas(t *testing.T) {
	ctx := context.Background()
	p := unsetPair(t)
	l := p.link(t)

	stg, _ := p.stg.Instance(ctx)
	prd, _ := p.prd.Instance(ctx)
	if stg.Role != RoleStaging || prd.Role != RoleProduction {
		t.Fatalf("roles = %q here, %q there; want staging and production", stg.Role, prd.Role)
	}
	// Each side already shows the other by its new role.
	if l.Role != RoleProduction || l.Name != "Production" {
		t.Fatalf("link = %q %q, want Production", l.Name, l.Role)
	}
	peers, _ := p.prd.Peers(ctx)
	if len(peers) != 1 || peers[0].Name != "Staging" {
		t.Fatalf("peers = %+v, want one named Staging", peers)
	}
}

func TestConnectingKeepsARoleSomeoneChose(t *testing.T) {
	ctx := context.Background()
	p := unsetPair(t)
	if _, err := p.stg.SetInstance(ctx, Instance{Name: "Dev box", Role: RoleDevelopment}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.prd.SetInstance(ctx, Instance{Role: RoleStaging}); err != nil {
		t.Fatal(err)
	}
	p.link(t)

	stg, _ := p.stg.Instance(ctx)
	prd, _ := p.prd.Instance(ctx)
	if stg != (Instance{Name: "Dev box", Role: RoleDevelopment}) || prd.Role != RoleStaging {
		t.Fatalf("roles changed: %+v here, %+v there", stg, prd)
	}
}

func TestMatchingSaysWhichConnectionsComeWithTheRelease(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	p.stgStore.sources["s-api"] = storage.Source{ID: "s-api", Name: "employees-api", Type: "http", VHost: "default",
		Config: map[string]string{"url": "https://hr.example.com", "token": "{{secret.HR_TOKEN}}"}}
	p.stgStore.sources["s-db"] = storage.Source{ID: "s-db", Name: "payroll-db", Type: "postgres", VHost: "default",
		Config: map[string]string{"host": "db", "password": "hunter2"}}
	p.stgStore.sinks["k-wh"] = storage.Sink{ID: "k-wh", Name: "warehouse", Type: "snowflake", VHost: "default"}
	p.prdStore.sinks["p-wh"] = storage.Sink{ID: "p-wh", Name: "warehouse", Type: "snowflake", VHost: "default"}
	l := p.link(t)

	rows, err := p.stg.Matching(ctx, l.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	row := func(id string) MatchRow {
		i := slices.IndexFunc(rows, func(r MatchRow) bool { return r.ID == id })
		if i < 0 {
			t.Fatalf("no row for %s in %+v", id, rows)
		}
		return rows[i]
	}
	if r := row("s-api"); !r.Travels || len(r.PlainKeys) != 0 {
		t.Fatalf("employees-api = %+v, want it to travel", r)
	}
	if r := row("s-db"); r.Travels || !slices.Equal(r.PlainKeys, []string{"password"}) {
		t.Fatalf("payroll-db = %+v, want it held back by its plain password", r)
	}
	if r := row("k-wh"); r.Travels || r.Status != MatchMatched {
		t.Fatalf("warehouse = %+v, want matched", r)
	}
	// Settings never go back to the browser through matching.
	for _, r := range rows {
		if r.Kind == "" {
			t.Fatalf("row without a kind: %+v", r)
		}
	}
}

func TestMatchingAgainstAnOlderInstanceOffersNoTravel(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	p.stgStore.sources["s-api"] = storage.Source{ID: "s-api", Name: "employees-api", Type: "http", VHost: "default"}
	l := p.link(t)
	p.remote.old = true

	rows, err := p.stg.Matching(ctx, l.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Travels {
		t.Fatalf("rows = %+v, want employees-api to need a match", rows)
	}
}
