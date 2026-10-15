// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package ml

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// fakeRecommender answers MLRecommend too, recording the request.
type fakeRecommender struct {
	fakeModels
	req   map[string]any
	items []map[string]any
}

func (f *fakeRecommender) MLRecommend(_ context.Context, vhost, model string, req map[string]any) ([]map[string]any, error) {
	f.vhost, f.model, f.req = vhost, model, req
	return f.items, f.err
}

var twoItems = []map[string]any{{"item": "sku-1", "score": 0.9}, {"item": "sku-2", "score": 0.4, "reason": "popular"}}

func TestRecommendModeReadsTheUserFieldAndWritesTheItems(t *testing.T) {
	reg := &fakeRecommender{items: twoItems}
	out, err := run(t, reg, "tenant-a", map[string]any{
		"model": "recs", "mode": "recommend", "idField": "customer.id", "k": "5",
		"excludeSeen": false, "outputField": "suggested",
	}, map[string]any{"customer": map[string]any{"id": 1000.0}})
	if err != nil {
		t.Fatal(err)
	}
	if reg.vhost != "tenant-a" || reg.model != "recs" || reg.rows != nil {
		t.Errorf("called %s/%s with rows %v", reg.vhost, reg.model, reg.rows)
	}
	if want := map[string]any{"user": 1000.0, "k": 5, "exclude_seen": false}; !reflect.DeepEqual(reg.req, want) {
		t.Errorf("request = %v, want %v", reg.req, want)
	}
	if got := out.Data()["suggested"]; !reflect.DeepEqual(got, twoItems) {
		t.Errorf("suggested = %v", got)
	}
}

func TestRecommendModeForAnItemAndItsDefaults(t *testing.T) {
	reg := &fakeRecommender{items: twoItems}
	out, err := run(t, reg, "", map[string]any{
		"model": "recs", "mode": "recommend", "recommendFor": "item", "idField": "sku",
	}, map[string]any{"sku": "sku-0"})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"item": "sku-0"}; !reflect.DeepEqual(reg.req, want) {
		t.Errorf("request = %v, want %v: k and exclude_seen left to the model's defaults", reg.req, want)
	}
	if _, ok := out.Data()[defaultRecommendField]; !ok {
		t.Errorf("no %q field: %v", defaultRecommendField, out.Data())
	}
}

func TestRecommendModeRefusals(t *testing.T) {
	cases := []struct {
		name   string
		reg    any
		cfg    map[string]any
		fields map[string]any
		want   string
	}{
		{"no id field", &fakeRecommender{}, map[string]any{"model": "recs", "mode": "recommend"}, nil, "idField"},
		{"the field is missing", &fakeRecommender{}, map[string]any{"model": "recs", "mode": "recommend", "idField": "uid"},
			map[string]any{"other": 1}, `no field "uid"`},
		{"a bad k", &fakeRecommender{}, map[string]any{"model": "recs", "mode": "recommend", "idField": "uid", "k": "many"},
			map[string]any{"uid": "u"}, "k"},
		{"a bad target", &fakeRecommender{}, map[string]any{"model": "recs", "mode": "recommend", "idField": "uid",
			"recommendFor": "shop"}, map[string]any{"uid": "u"}, "recommendFor"},
		{"exclude_seen for an item", &fakeRecommender{}, map[string]any{"model": "recs", "mode": "recommend", "idField": "uid",
			"recommendFor": "item", "excludeSeen": true}, map[string]any{"uid": "u"}, "excludeSeen"},
		{"an unknown mode", &fakeRecommender{}, map[string]any{"model": "recs", "mode": "guess"}, nil, "mode"},
		{"a registry without recommendations", &fakeModels{}, map[string]any{"model": "recs", "mode": "recommend", "idField": "uid"},
			map[string]any{"uid": "u"}, "recommend"},
		{"the model refuses", &fakeRecommender{fakeModels: fakeModels{err: errors.New("not a recommendation model")}},
			map[string]any{"model": "recs", "mode": "recommend", "idField": "uid"}, map[string]any{"uid": "u"}, "not a recommendation model"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(t, tc.reg, "", tc.cfg, tc.fields)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}
