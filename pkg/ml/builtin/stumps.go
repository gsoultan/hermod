// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package builtin

import (
	"cmp"
	"slices"
)

// stumpLeaf is a leaf of a one-feature tree: the sorted rows idx[from:to],
// and its best split, idx[from:at] and idx[at:to] (at is -1 for none), with
// how much that split lowers the impurity, weighted by rows.
type stumpLeaf struct {
	from, to, at int
	gain         float64
}

// stumps grows a one-feature classification tree over rows sorted by x.
type stumps struct {
	x, y []float64
	idx  []int
	k    int
}

// fitStumps grows a one-feature tree best first, as scikit-learn does with
// max_leaf_nodes: the leaf whose best split lowers the Gini impurity most is
// split next, until there are maxLeaves leaves or no split helps.
func fitStumps(x, y []float64, rows []int, k, maxLeaves int) intervals {
	s := stumps{x: x, y: y, idx: slices.Clone(rows), k: k}
	slices.SortStableFunc(s.idx, func(a, b int) int { return cmp.Compare(x[a], x[b]) })
	leaves := []stumpLeaf{s.best(0, len(s.idx))}
	for len(leaves) < maxLeaves {
		pick := -1
		for i, l := range leaves {
			if l.at >= 0 && (pick < 0 || l.gain > leaves[pick].gain) {
				pick = i
			}
		}
		if pick < 0 {
			break
		}
		l := leaves[pick]
		leaves = slices.Replace(leaves, pick, pick+1, s.best(l.from, l.at), s.best(l.at, l.to))
	}
	var t intervals
	for i, l := range leaves {
		t.answers = append(t.answers, argmaxInt(s.counts(l.from, l.to)))
		if i > 0 {
			t.cuts = append(t.cuts, (x[s.idx[l.from-1]]+x[s.idx[l.from]])/2)
		}
	}
	return t
}

func (s stumps) counts(from, to int) []int {
	cnt := make([]int, s.k)
	for _, r := range s.idx[from:to] {
		cnt[int(s.y[r])]++
	}
	return cnt
}

// best is the leaf of rows [from, to) with its best split, between two
// different values of x.
func (s stumps) best(from, to int) stumpLeaf {
	l := stumpLeaf{from: from, to: to, at: -1}
	n := to - from
	right := s.counts(from, to)
	parent := float64(n) * gini(right, n)
	left := make([]int, s.k)
	for p := from; p < to-1; p++ {
		c := int(s.y[s.idx[p]])
		left[c]++
		right[c]--
		if s.x[s.idx[p]] == s.x[s.idx[p+1]] {
			continue
		}
		nl := p + 1 - from
		gain := parent - float64(nl)*gini(left, nl) - float64(n-nl)*gini(right, n-nl)
		if gain > l.gain+1e-12 {
			l.gain, l.at = gain, p+1
		}
	}
	return l
}

// gini is the Gini impurity of n rows with these class counts.
func gini(cnt []int, n int) float64 {
	if n == 0 {
		return 0
	}
	s := 0.0
	for _, c := range cnt {
		p := float64(c) / float64(n)
		s += p * p
	}
	return 1 - s
}
