// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package recommend

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// show renders items as "i1:0.8 i3:0.3 i0:1(popular)" for compact comparison.
func show(items []Item) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = fmt.Sprintf("%s:%.4g", it.Item, it.Score)
		if it.Reason != "" {
			parts[i] += "(" + it.Reason + ")"
		}
	}
	return strings.Join(parts, " ")
}

func TestForUser(t *testing.T) {
	als, nbr := mustParse(t, factorsArtifact()), mustParse(t, neighborsArtifact())
	cases := []struct {
		name        string
		m           *Model
		user        string
		k           int
		excludeSeen bool
		want        string
		known       bool
	}{
		// u1 has i0 and i2; scores are its factors' dot products.
		{"als excludes seen and tops up from what is left", als, "u1", 10, true, "i1:0.8 i3:0.3", true},
		{"als with seen items", als, "u1", 10, false, "i0:0.9 i1:0.8 i3:0.3 i2:0.1", true},
		{"als cut to k", als, "u1", 1, true, "i1:0.8", true},
		{"als person with nothing seen", als, "u2", 3, true, "i2:0.9 i3:0.3 i1:0.2", true},
		{"unknown person gets popular items", als, "nobody", 10, true, "i0:1(popular) i2:0.5(popular) i3:0.5(popular)", false},
		// Neighbor scores add up over the items a person has.
		{"neighbors exclude seen", nbr, "u1", 10, true, "i1:0.4 i3:0.3", true},
		{"neighbors tie broken by item order", nbr, "u1", 2, false, "i0:0.8 i2:0.8", true},
		{"neighbors topped up from popular", nbr, "u3", 10, true, "i2:0.6 i0:1(popular)", true},
		{"no personal score at all", nbr, "u2", 2, true, "i0:1(popular) i2:0.5(popular)", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, known := tc.m.ForUser(tc.user, tc.k, tc.excludeSeen)
			if show(got) != tc.want || known != tc.known {
				t.Errorf("ForUser(%q, %d, %v) = %s, %v; want %s, %v", tc.user, tc.k, tc.excludeSeen, show(got), known, tc.want, tc.known)
			}
		})
	}
}

func TestSimilar(t *testing.T) {
	als, nbr := mustParse(t, factorsArtifact()), mustParse(t, neighborsArtifact())
	cases := []struct {
		name  string
		m     *Model
		item  string
		k     int
		want  string
		known bool
	}{
		{"als by cosine of item factors", als, "i0", 3, "i1:0.991 i3:0.7809 i2:0.2195", true},
		{"neighbors as stored, then popular", nbr, "i0", 10, "i2:0.8 i1:0.4 i3:0.5(popular)", true},
		{"neighbors cut to k", nbr, "i0", 1, "i2:0.8", true},
		{"an item with no neighbors gets popular items", nbr, "i1", 2, "i0:1(popular) i2:0.5(popular)", true},
		{"unknown item gets popular items", nbr, "nothing", 1, "i0:1(popular)", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, known := tc.m.Similar(tc.item, tc.k)
			if show(got) != tc.want || known != tc.known {
				t.Errorf("Similar(%q, %d) = %s, %v; want %s, %v", tc.item, tc.k, show(got), known, tc.want, tc.known)
			}
		})
	}
}

func TestKIsDefaultedAndCapped(t *testing.T) {
	cases := map[int]int{0: DefaultK, -3: DefaultK, 1: 1, 37: 37, MaxK: MaxK, MaxK + 1: MaxK, math.MaxInt: MaxK}
	for in, want := range cases {
		if got := ClampK(in); got != want {
			t.Errorf("ClampK(%d) = %d, want %d", in, got, want)
		}
	}
	// A model of 250 items answers at most MaxK of them.
	b := factorsArtifact()
	items := make([]string, 250)
	b.itemFactors = make([]float32, 2*len(items))
	for i := range items {
		items[i] = fmt.Sprintf("x%d", i)
		b.itemFactors[2*i] = float32(i)
	}
	b.header["items"] = items
	m := mustParse(t, b)
	got, _ := m.ForUser("u2", 1000, true)
	if len(got) != MaxK {
		t.Fatalf("ForUser with k=1000 returned %d items, want %d", len(got), MaxK)
	}
	if got[0].Item != "x0" {
		// u2 = (0, 1) scores every item 0, so the tie is broken by item order.
		t.Errorf("first item %q, want x0", got[0].Item)
	}
}

func TestIDsAcceptNumbers(t *testing.T) {
	cases := map[string]any{"u42": "u42", "42": 42.0, "42.5": 42.5, "-7": float64(-7), "true": true, "1e+21": 1e21}
	for want, in := range cases {
		got, ok := ID(in)
		if !ok || got != want {
			t.Errorf("ID(%v) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []any{nil, "", map[string]any{}, []any{1}, math.NaN()} {
		if got, ok := ID(in); ok {
			t.Errorf("ID(%v) = %q, want refused", in, got)
		}
	}
}
