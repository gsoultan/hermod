// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package learn

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sort"
)

// maxBins is how many bins a number feature is cut into for finding splits:
// each distinct value its own bin when it has at most this many, quantiles
// otherwise, the way histogram gradient boosting works.
const maxBins = 255

// Tree is a decision tree, its nodes in arrays, node 0 the root. A node with
// Feature -1 is a leaf, whose values (Out of them) start at Values[Left[i]*Out].
// Otherwise Feature below the number of number features splits on that
// number, x <= Threshold going left; a higher Feature is the text feature
// Feature minus that count, the rows of category Threshold going right and
// the rest left.
type Tree struct {
	Feature   []int32   `json:"feature"`
	Threshold []float64 `json:"threshold"`
	Left      []int32   `json:"left"`
	Right     []int32   `json:"right"`
	Values    []float64 `json:"values"`
}

// leaf returns the values of the leaf row i of x falls in.
func (t *Tree) leaf(x *Matrix, i, out int) []float64 {
	nNum := int32(len(x.Num))
	n := int32(0)
	for {
		f := t.Feature[n]
		switch {
		case f < 0:
			at := int(t.Left[n]) * out
			return t.Values[at : at+out]
		case f < nNum:
			if x.Num[f][i] <= t.Threshold[n] {
				n = t.Left[n]
			} else {
				n = t.Right[n]
			}
		default:
			if x.Cat[f-nNum][i] == int32(t.Threshold[n]) {
				n = t.Right[n]
			} else {
				n = t.Left[n]
			}
		}
	}
}

// check refuses a tree that could index out of range or loop: every child
// comes after its parent, and every split names a feature, and a category,
// the model has.
func (t *Tree) check(nNum int, catSize []int, out int) error {
	n := len(t.Feature)
	if n == 0 || len(t.Threshold) != n || len(t.Left) != n || len(t.Right) != n || len(t.Values)%out != 0 {
		return errors.New("a tree's arrays do not match")
	}
	leaves := len(t.Values) / out
	for i := range n {
		if err := t.checkNode(i, nNum, catSize, leaves); err != nil {
			return err
		}
	}
	if !finite(t.Values, t.Threshold) {
		return errors.New("a tree holds a value that is not finite")
	}
	return nil
}

// checkNode refuses node i when it splits on a feature or category the
// model does not have, or points at a node or leaf out of range. A child
// always comes after its parent, so a walk cannot loop.
func (t *Tree) checkNode(i, nNum int, catSize []int, leaves int) error {
	f := int(t.Feature[i])
	switch {
	case f < 0:
		if t.Left[i] < 0 || int(t.Left[i]) >= leaves {
			return fmt.Errorf("leaf %d points at values %d of %d", i, t.Left[i], leaves)
		}
		return nil
	case f < nNum:
		if math.IsNaN(t.Threshold[i]) {
			return fmt.Errorf("node %d splits at NaN", i)
		}
	case f < nNum+len(catSize):
		c := t.Threshold[i]
		if c != math.Trunc(c) || c < 0 || int(c) >= catSize[f-nNum] {
			return fmt.Errorf("node %d splits on category %v of %d", i, c, catSize[f-nNum])
		}
	default:
		return fmt.Errorf("node %d splits on feature %d of %d", i, f, nNum+len(catSize))
	}
	n := len(t.Feature)
	if int(t.Left[i]) <= i || int(t.Right[i]) <= i || int(t.Left[i]) >= n || int(t.Right[i]) >= n {
		return fmt.Errorf("node %d has children %d and %d", i, t.Left[i], t.Right[i])
	}
	return nil
}

// binned is the training matrix with each number feature cut into bins.
type binned struct {
	x     *Matrix
	codes [][]uint8
	// cut[f][b] is the threshold between bin b and bin b+1 of feature f:
	// x <= cut[f][b] holds exactly for the training values in bins 0..b.
	cut [][]float64
}

func binMatrix(x *Matrix) *binned {
	b := &binned{x: x, codes: make([][]uint8, len(x.Num)), cut: make([][]float64, len(x.Num))}
	for f, col := range x.Num {
		vals := slices.Clone(col)
		slices.Sort(vals)
		vals = slices.Compact(vals)
		var edges []float64 // upper value of each bin
		if len(vals) <= maxBins {
			edges = vals
			b.cut[f] = make([]float64, max(0, len(vals)-1))
			for i := range b.cut[f] {
				b.cut[f][i] = vals[i] + (vals[i+1]-vals[i])/2
			}
		} else {
			sorted := slices.Clone(col)
			slices.Sort(sorted)
			for q := 1; q <= maxBins; q++ {
				v := sorted[min(len(sorted)-1, q*len(sorted)/maxBins)]
				if len(edges) == 0 || v > edges[len(edges)-1] {
					edges = append(edges, v)
				}
			}
			edges[len(edges)-1] = vals[len(vals)-1]
			b.cut[f] = slices.Clone(edges[:len(edges)-1])
		}
		codes := make([]uint8, len(col))
		for i, v := range col {
			codes[i] = uint8(sort.SearchFloat64s(edges, v))
		}
		b.codes[f] = codes
	}
	return b
}

// treeParams are how a tree grows. The statistic each row brings is S
// values long: a classification's one-hot class (weighted), or a
// regression's weight, weighted value and weighted square.
type treeParams struct {
	maxDepth int
	minLeaf  int
	// mtry is how many columns (number features and categories) each split
	// considers, drawn at random; 0 means all of them.
	mtry    int
	classes int
	// leafValue is the values a leaf holds, from the rows in it.
	leafValue func(rows []int) []float64
	out       int
}

// grower grows one tree.
type grower struct {
	b   *binned
	p   treeParams
	y   []float64
	w   []float64
	rng *rand.Rand
	s   int // stats per row
	t   *Tree

	hist  []float64
	count []int32
}

func growTree(b *binned, y, w []float64, rows []int, p treeParams, rng *rand.Rand) *Tree {
	g := &grower{b: b, p: p, y: y, w: w, rng: rng, t: &Tree{}}
	g.s = 3
	if p.classes > 0 {
		g.s = p.classes
	}
	g.grow(rows, 0)
	return g.t
}

func (g *grower) weight(i int) float64 {
	if g.w == nil {
		return 1
	}
	return g.w[i]
}

// addStat adds row i's statistic into dst.
func (g *grower) addStat(dst []float64, i int) {
	w := g.weight(i)
	if g.p.classes > 0 {
		dst[int(g.y[i])] += w
		return
	}
	dst[0] += w
	dst[1] += w * g.y[i]
	dst[2] += w * g.y[i] * g.y[i]
}

// gainScore is what a side contributes to the split's score; the split with
// the highest left plus right is the one with the least impurity.
func (g *grower) gainScore(st []float64) float64 {
	if g.p.classes > 0 {
		tot, sq := 0.0, 0.0
		for _, v := range st {
			tot += v
			sq += v * v
		}
		if tot <= 0 {
			return 0
		}
		return sq / tot
	}
	if st[0] <= 0 {
		return 0
	}
	return st[1] * st[1] / st[0]
}

type split struct {
	feature   int
	threshold float64
	bin       int
	score     float64
	ok        bool
}

func (g *grower) grow(rows []int, depth int) int32 {
	node := int32(len(g.t.Feature))
	g.t.Feature = append(g.t.Feature, -1)
	g.t.Threshold = append(g.t.Threshold, 0)
	g.t.Left = append(g.t.Left, 0)
	g.t.Right = append(g.t.Right, 0)

	total := make([]float64, g.s)
	for _, i := range rows {
		g.addStat(total, i)
	}
	best := split{}
	if len(rows) >= 2*g.p.minLeaf && len(rows) >= 2 && (g.p.maxDepth <= 0 || depth < g.p.maxDepth) && !g.pure(total) {
		best = g.bestSplit(rows, total)
	}
	if !best.ok {
		g.t.Left[node] = int32(len(g.t.Values) / g.p.out)
		g.t.Values = append(g.t.Values, g.p.leafValue(rows)...)
		return node
	}
	left, right := g.partition(rows, best)
	g.t.Feature[node] = int32(best.feature)
	g.t.Threshold[node] = best.threshold
	l := g.grow(left, depth+1)
	r := g.grow(right, depth+1)
	g.t.Left[node], g.t.Right[node] = l, r
	return node
}

// pure reports whether a node has nothing to split: one class, or one value.
func (g *grower) pure(total []float64) bool {
	if g.p.classes > 0 {
		nonzero := 0
		for _, v := range total {
			if v > 0 {
				nonzero++
			}
		}
		return nonzero <= 1
	}
	if total[0] <= 0 {
		return true
	}
	mean := total[1] / total[0]
	return total[2]/total[0]-mean*mean <= 1e-12*math.Max(1, mean*mean)
}

// candidates are the columns a split may use: number features, then each
// category of each text feature, numbered in that order. With mtry set, a
// random mtry of them, in ascending order.
func (g *grower) candidates() []int {
	x := g.b.x
	total := x.Width()
	if g.p.mtry <= 0 || g.p.mtry >= total {
		all := make([]int, total)
		for i := range all {
			all[i] = i
		}
		return all
	}
	picked := g.rng.Perm(total)[:g.p.mtry]
	slices.Sort(picked)
	return picked
}

func (g *grower) bestSplit(rows []int, total []float64) split {
	x := g.b.x
	nNum := len(x.Num)
	base := g.gainScore(total)
	best := split{score: base + 1e-12*math.Max(1, math.Abs(base))}
	left, right := make([]float64, g.s), make([]float64, g.s)
	catHist := map[int][]float64{}
	catCount := map[int][]int32{}
	for _, col := range g.candidates() {
		if col < nNum {
			g.numberSplit(rows, col, total, left, right, &best)
			continue
		}
		// Which text feature, and which of its categories.
		f, cat := 0, col-nNum
		for f < len(x.CatSize) && cat >= x.CatSize[f] {
			cat -= x.CatSize[f]
			f++
		}
		h, ok := catHist[f]
		if !ok {
			h, catCount[f] = g.categoryHist(rows, f)
			catHist[f] = h
		}
		cnt := catCount[f]
		in := int(cnt[cat])
		if in < g.p.minLeaf || len(rows)-in < g.p.minLeaf {
			continue
		}
		copy(right, h[cat*g.s:(cat+1)*g.s])
		for k := range left {
			left[k] = total[k] - right[k]
		}
		if s := g.gainScore(left) + g.gainScore(right); s > best.score {
			best = split{feature: nNum + f, threshold: float64(cat), score: s, ok: true}
		}
	}
	return best
}

func (g *grower) numberSplit(rows []int, f int, total, left, right []float64, best *split) {
	cuts := g.b.cut[f]
	if len(cuts) == 0 {
		return
	}
	nb := len(cuts) + 1
	need := nb * g.s
	if cap(g.hist) < need {
		g.hist = make([]float64, need)
	}
	if cap(g.count) < nb {
		g.count = make([]int32, nb)
	}
	hist, count := g.hist[:need], g.count[:nb]
	clear(hist)
	clear(count)
	codes := g.b.codes[f]
	for _, i := range rows {
		b := int(codes[i])
		g.addStat(hist[b*g.s:(b+1)*g.s], i)
		count[b]++
	}
	clear(left)
	n, seen := len(rows), 0
	for b := 0; b < nb-1; b++ {
		for k := range left {
			left[k] += hist[b*g.s+k]
		}
		seen += int(count[b])
		if count[b] == 0 {
			continue
		}
		if seen < g.p.minLeaf {
			continue
		}
		if n-seen < g.p.minLeaf {
			break
		}
		for k := range right {
			right[k] = total[k] - left[k]
		}
		if s := g.gainScore(left) + g.gainScore(right); s > best.score {
			*best = split{feature: f, threshold: cuts[b], bin: b, score: s, ok: true}
		}
	}
}

func (g *grower) categoryHist(rows []int, f int) ([]float64, []int32) {
	size := g.b.x.CatSize[f]
	h := make([]float64, size*g.s)
	cnt := make([]int32, size)
	codes := g.b.x.Cat[f]
	for _, i := range rows {
		if c := codes[i]; c >= 0 {
			g.addStat(h[int(c)*g.s:(int(c)+1)*g.s], i)
			cnt[c]++
		}
	}
	return h, cnt
}

// partition splits rows into those going left and right, in their order.
func (g *grower) partition(rows []int, s split) (left, right []int) {
	x := g.b.x
	nNum := len(x.Num)
	left = make([]int, 0, len(rows))
	right = make([]int, 0, len(rows)/2)
	for _, i := range rows {
		var goRight bool
		if s.feature < nNum {
			goRight = int(g.b.codes[s.feature][i]) > s.bin
		} else {
			goRight = x.Cat[s.feature-nNum][i] == int32(s.threshold)
		}
		if goRight {
			right = append(right, i)
		} else {
			left = append(left, i)
		}
	}
	return left, right
}
