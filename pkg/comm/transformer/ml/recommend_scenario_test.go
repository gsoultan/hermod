// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package ml

import (
	"reflect"
	"testing"
)

func TestRecommendModeForAScenarioSubject(t *testing.T) {
	reg := &fakeRecommender{items: []map[string]any{{"id": "C1", "score": 0.7, "rank": 1, "plan_date": "2026-10-13"}}}
	out, err := run(t, reg, "tenant-a", map[string]any{
		"model": "plan", "mode": "recommend", "recommendFor": "salesman", "idField": "rep", "periodDays": "5", "k": 12,
		"rules": `[{"kind":"capacity","per_day":4}]`,
	}, map[string]any{"rep": "R07"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"for": map[string]any{"salesman": "R07"}, "period_days": 5, "k": 12,
		"rules": []any{map[string]any{"kind": "capacity", "per_day": 4.0}}}
	if !reflect.DeepEqual(reg.req, want) {
		t.Errorf("request = %v, want %v", reg.req, want)
	}
	if got := out.Data()[defaultRecommendField]; !reflect.DeepEqual(got, reg.items) {
		t.Errorf("recommendations = %v", got)
	}
	for _, to := range []string{"customer", "product"} {
		reg := &fakeRecommender{}
		if _, err := run(t, reg, "", map[string]any{"model": "m", "mode": "recommend", "recommendFor": to, "idField": "id",
			"excludeSeen": "false"}, map[string]any{"id": 7.0}); err != nil {
			t.Fatal(err)
		}
		if want := map[string]any{"for": map[string]any{to: 7.0}, "exclude_seen": false}; !reflect.DeepEqual(reg.req, want) {
			t.Errorf("%s: request = %v, want %v", to, reg.req, want)
		}
	}
	// withinDays asks a reorder model for the customers due in that window.
	for _, to := range []string{"salesman", "product", "customer"} {
		reg := &fakeRecommender{}
		if _, err := run(t, reg, "", map[string]any{"model": "due", "mode": "recommend", "recommendFor": to, "idField": "id",
			"withinDays": "14"}, map[string]any{"id": "x"}); err != nil {
			t.Fatal(err)
		}
		if want := map[string]any{"for": map[string]any{to: "x"}, "within_days": 14}; !reflect.DeepEqual(reg.req, want) {
			t.Errorf("%s: request = %v, want %v", to, reg.req, want)
		}
	}
	// segment and size keep one customer category and size of a list.
	reg = &fakeRecommender{}
	if _, err := run(t, reg, "", map[string]any{"model": "due", "mode": "recommend", "recommendFor": "salesman", "idField": "id",
		"withinDays": 14, "segment": " Retail ", "size": "large"}, map[string]any{"id": "R1"}); err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"for": map[string]any{"salesman": "R1", "segment": "Retail", "size": "large"}, "within_days": 14}; !reflect.DeepEqual(reg.req, want) {
		t.Errorf("category request = %v, want %v", reg.req, want)
	}
	// priceTier, channel and region keep one price tier, channel and region
	// of a list of customers.
	reg = &fakeRecommender{}
	if _, err := run(t, reg, "", map[string]any{"model": "who", "mode": "recommend", "recommendFor": "product", "idField": "id",
		"priceTier": " Gold ", "channel": "Retail", "region": "N"}, map[string]any{"id": "S1"}); err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"for": map[string]any{"product": "S1", "price_tier": "Gold", "channel": "Retail", "region": "N"}}; !reflect.DeepEqual(reg.req, want) {
		t.Errorf("tier request = %v, want %v", reg.req, want)
	}
	// brand and subcategory keep one brand and subcategory of a list of products.
	reg = &fakeRecommender{}
	if _, err := run(t, reg, "", map[string]any{"model": "offer", "mode": "recommend", "recommendFor": "customer", "idField": "id",
		"brand": " Acme ", "subcategory": "Soap"}, map[string]any{"id": "C1"}); err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"for": map[string]any{"customer": "C1", "brand": "Acme", "subcategory": "Soap"}}; !reflect.DeepEqual(reg.req, want) {
		t.Errorf("brand request = %v, want %v", reg.req, want)
	}
	for _, cfg := range []map[string]any{
		{"recommendFor": "user", "brand": "Acme"},
		{"recommendFor": "item", "subcategory": "Soap"},
		{"recommendFor": "user", "segment": "Retail"},
		{"recommendFor": "item", "size": "large"},
		{"recommendFor": "user", "periodDays": 7},
		{"recommendFor": "salesman", "periodDays": 400},
		{"recommendFor": "user", "withinDays": 14},
		{"recommendFor": "salesman", "withinDays": 367},
		{"recommendFor": "salesman", "withinDays": "soon"},
		{"recommendFor": "item", "rules": "[]"},
		{"recommendFor": "salesman", "rules": "{not json"},
	} {
		cfg["model"], cfg["mode"], cfg["idField"] = "m", "recommend", "id"
		if _, err := run(t, &fakeRecommender{}, "", cfg, map[string]any{"id": "x"}); err == nil {
			t.Errorf("%v: accepted", cfg)
		}
	}
}
