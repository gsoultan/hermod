// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package recommend

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// FuzzParse feeds Parse arbitrary bytes, seeded with valid artifacts. Parse
// must refuse what it cannot read with ErrInvalid and never panic, and a model
// it accepts must answer any call within bounds: at most k items, each an item
// of the model, with a finite score.
func FuzzParse(f *testing.F) {
	f.Add(factorsArtifact().encode(f))
	f.Add(neighborsArtifact().encode(f))
	for _, algorithm := range []string{AlgorithmALS, AlgorithmItemSimilarity} {
		raw, err := os.ReadFile(filepath.Join("testdata", algorithm, "model.rec"))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	for _, name := range []string{"visits", "products", "reorder", "audience_als", "similar"} {
		raw, err := os.ReadFile(filepath.Join("testdata", "scenario", name+".rec"))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	f.Add([]byte("HREC\x01\x00\x00\x00\x02\x00\x00\x00{}"))
	f.Add([]byte("HREC\x02\x00\x00\x00\x02\x00\x00\x00{}"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		m, err := Parse(raw)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Parse error %v is not ErrInvalid", err)
			}
			return
		}
		ids := append([]string{"", "nobody"}, m.users...)
		items := append([]string{"", "nothing"}, m.items...)
		for _, k := range []int{-1, 0, 1, 3, MaxK + 5} {
			for _, u := range ids[:min(len(ids), 6)] {
				for _, exclude := range []bool{true, false} {
					got, _ := m.ForUser(u, k, exclude)
					checkItems(t, m, got, k)
				}
			}
			for _, it := range items[:min(len(items), 6)] {
				got, _ := m.Similar(it, k)
				checkItems(t, m, got, k)
			}
		}
		answerAnything(t, m, ids, items)
	})
}

// answerAnything puts every kind of scenario call to a parsed model: each
// must answer within bounds or refuse with ErrBadQuery, never panic.
func answerAnything(t *testing.T, m *Model, ids, items []string) {
	t.Helper()
	salesmen := append([]string{"nobody"}, m.Salesmen()...)
	today := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var queries []Query
	for _, s := range salesmen[:min(len(salesmen), 3)] {
		queries = append(queries, Query{Salesman: s}, Query{Salesman: s, K: 3, Rules: []Rule{{Kind: RuleDiversity, Field: "customers.segment", MaxPer: 1}}})
	}
	for _, u := range ids[:min(len(ids), 4)] {
		queries = append(queries, Query{Customer: u, K: 5, ExcludeSeen: true}, Query{Customers: []string{u, "x"}})
	}
	for _, it := range items[:min(len(items), 4)] {
		queries = append(queries, Query{Product: it, K: 4})
	}
	for _, q := range queries {
		q.Today = today
		res, err := m.Answer(q)
		if err != nil {
			if !errors.Is(err, ErrBadQuery) && !errors.Is(err, ErrBadRule) {
				t.Fatalf("Answer(%+v): %v", q, err)
			}
			continue
		}
		for _, r := range res {
			if len(r.Items) > max(MaxPlan, MaxK) {
				t.Fatalf("%d items", len(r.Items))
			}
			for _, p := range r.Items {
				if p.Score != p.Score || p.Rank < 1 {
					t.Fatalf("pick %+v", p)
				}
			}
		}
	}
}

func checkItems(t *testing.T, m *Model, got []Item, k int) {
	t.Helper()
	if len(got) > ClampK(k) {
		t.Fatalf("%d items for k=%d", len(got), k)
	}
	seen := map[string]bool{}
	for _, it := range got {
		if _, ok := m.itemIndex[it.Item]; !ok {
			t.Fatalf("recommended %q, which is not an item of the model", it.Item)
		}
		if seen[it.Item] {
			t.Fatalf("recommended %q twice", it.Item)
		}
		seen[it.Item] = true
		if it.Score != it.Score {
			t.Fatalf("item %q has a NaN score", it.Item)
		}
	}
}
