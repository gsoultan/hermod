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
	"math/rand/v2"
	"testing"
)

// synthetic is an artifact of the given size with random factors or
// neighbors, each person having seen `seen` items.
func synthetic(tb testing.TB, users, items, factors, neighbors, seen int) *Model {
	tb.Helper()
	rng := rand.New(rand.NewPCG(1, 2))
	ids := func(prefix string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%s%d", prefix, i)
		}
		return out
	}
	floats := func(n int) []float32 {
		out := make([]float32, n)
		for i := range out {
			out[i] = rng.Float32()
		}
		return out
	}
	b := &built{
		magic: "HREC", version: 1,
		header: map[string]any{
			"users": ids("u", users), "items": ids("i", items), "factors": factors, "neighbors": neighbors,
			"seen": users * seen, "popular": 100,
		},
		seenOff: make([]uint32, users+1),
	}
	if factors > 0 {
		b.header["algorithm"] = AlgorithmALS
		b.userFactors, b.itemFactors = floats(users*factors), floats(items*factors)
	} else {
		b.header["algorithm"] = AlgorithmItemSimilarity
		for i := 0; i < items; i++ {
			for s := 0; s < neighbors; s++ {
				b.nbrItems = append(b.nbrItems, uint32((i+1+s)%items))
			}
		}
		b.nbrScores = floats(items * neighbors)
	}
	stride := items / seen
	for u := 0; u < users; u++ {
		for s := 0; s < seen; s++ {
			b.seenItems = append(b.seenItems, uint32(s*stride+u%stride))
		}
		b.seenOff[u+1] = uint32((u + 1) * seen)
	}
	for i := 0; i < 100; i++ {
		b.popular = append(b.popular, uint32(i))
		b.popularScores = append(b.popularScores, 1/float32(i+1))
	}
	return mustParse(tb, b)
}

// BenchmarkForUser is one person's top 10 and top 100: a dot product over
// the whole catalogue for als, a sum over the neighbors of what they have for
// item_similarity.
func BenchmarkForUser(b *testing.B) {
	models := []struct {
		name string
		m    *Model
	}{
		{"als/items=10k/factors=32", synthetic(b, 1000, 10_000, 32, 0, 20)},
		{"als/items=100k/factors=64", synthetic(b, 100, 100_000, 64, 0, 20)},
		{"item_similarity/items=100k/neighbors=50", synthetic(b, 100, 100_000, 0, 50, 20)},
	}
	for _, mc := range models {
		for _, k := range []int{10, 100} {
			b.Run(fmt.Sprintf("%s/k=%d", mc.name, k), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if got, _ := mc.m.ForUser("u7", k, true); len(got) != k {
						b.Fatalf("got %d items", len(got))
					}
				}
			})
		}
	}
}

// BenchmarkSimilar is one item's 10 most similar items.
func BenchmarkSimilar(b *testing.B) {
	for _, mc := range []struct {
		name string
		m    *Model
	}{
		{"als/items=100k/factors=64", synthetic(b, 10, 100_000, 64, 0, 2)},
		{"item_similarity/items=100k/neighbors=50", synthetic(b, 10, 100_000, 0, 50, 2)},
	} {
		b.Run(mc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				mc.m.Similar("i42", 10)
			}
		})
	}
}

// BenchmarkParse is loading an artifact of 100k items and 64 factors (25 MB),
// done once per live version.
func BenchmarkParse(b *testing.B) {
	m := synthetic(b, 1000, 100_000, 64, 0, 20)
	bt := &built{magic: "HREC", version: 1, header: map[string]any{
		"algorithm": AlgorithmALS, "users": m.users, "items": m.items, "factors": 64, "neighbors": 0,
		"seen": len(m.seenItems), "popular": len(m.popular),
	}, userFactors: m.userFactors, itemFactors: m.itemFactors, seenOff: m.seenOff, seenItems: m.seenItems,
		popular: m.popular, popularScores: m.popularScores}
	raw := bt.encode(b)
	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Parse(raw); err != nil {
			b.Fatal(err)
		}
	}
}
