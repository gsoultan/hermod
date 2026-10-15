// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
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
	"math"
	"slices"
	"strconv"
)

// How many items a call returns: DefaultK when it does not say, at most MaxK.
const (
	DefaultK = 10
	MaxK     = 100
)

// ReasonPopular marks an item recommended because many people have it, not
// because of who is asking: for a person or item the model does not know, or
// to fill a list the model's own scores leave short.
const ReasonPopular = "popular"

// Item is one recommended item. Score is the model's: a dot product for als,
// a sum of similarities for item_similarity, a cosine similarity for similar
// items, and the share of the most popular item's count for a popular one.
type Item struct {
	Item   string  `json:"item"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason,omitempty"`
}

// ClampK is the k a call uses: DefaultK for zero or less, at most MaxK.
func ClampK(k int) int {
	if k <= 0 {
		return DefaultK
	}
	return min(k, MaxK)
}

// ID is the text a person or item id is matched on, as the worker turned the
// column's values into text (hermod_ml.datasets.to_text): a string as it is,
// true or false, and a whole number without ".0". It reports false for
// anything that cannot be an id: nothing, an empty string, an object or list,
// or a number that is not finite.
func ID(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, x != ""
	case bool:
		return strconv.FormatBool(x), true
	case int:
		return strconv.Itoa(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case float64:
		return pyFloat(x)
	case float32:
		return pyFloat(float64(x))
	}
	return "", false
}

// pyFloat writes f as Python's to_text does: whole numbers below 2^53 as
// integers, anything else as repr() would.
func pyFloat(f float64) (string, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", false
	}
	a := math.Abs(f)
	if f == math.Trunc(f) && a < 1<<53 {
		return strconv.FormatInt(int64(f), 10), true
	}
	if a >= 1e-4 && a < 1e16 {
		return strconv.FormatFloat(f, 'f', -1, 64), true
	}
	return strconv.FormatFloat(f, 'e', -1, 64), true
}

// Popular is the k most popular items.
func (m *Model) Popular(k int) []Item {
	k = min(ClampK(k), len(m.popular))
	out := make([]Item, k)
	for i := range out {
		out[i] = Item{Item: m.items[m.popular[i]], Score: float64(m.popularScores[i]), Reason: ReasonPopular}
	}
	return out
}

// ForUser is a person's top k items, best first, leaving out what they
// already have when excludeSeen is set. A person the model does not know gets
// the popular items, and known reports false. A list the model's own scores
// leave short is filled with popular items the person does not have.
func (m *Model) ForUser(user string, k int, excludeSeen bool) (items []Item, known bool) {
	k = ClampK(k)
	u, ok := m.userIndex[user]
	if !ok {
		return m.Popular(k), false
	}
	seen := m.seenBy(u)
	var exclude []uint32
	if excludeSeen {
		exclude = seen
	}
	var picked []scored
	if m.factors > 0 {
		picked = m.topFactors(m.userFactors[u*m.factors:(u+1)*m.factors], k, exclude)
	} else {
		picked = m.topNeighbors(seen, k, exclude)
	}
	return m.fill(picked, k, exclude, -1), true
}

// Similar is the k items most like item, best first. An item the model does
// not know gets the popular items, and known reports false; a list its
// neighbors leave short is filled with popular items.
func (m *Model) Similar(item string, k int) (items []Item, known bool) {
	k = ClampK(k)
	i, ok := m.itemIndex[item]
	if !ok {
		return m.Popular(k), false
	}
	var picked []scored
	if m.factors > 0 {
		picked = m.topCosine(i, k)
	} else {
		for s, j := range m.neighborsOf(i) {
			if j == empty || len(picked) == k {
				break
			}
			picked = append(picked, scored{item: j, score: float64(m.nbrScores[i*m.neighbors+s])})
		}
	}
	return m.fill(picked, k, nil, i), true
}

// fill renders the picked items and tops the list up to k from the popular
// items that are neither picked, excluded nor self.
func (m *Model) fill(picked []scored, k int, exclude []uint32, self int) []Item {
	out := make([]Item, 0, k)
	for _, p := range picked {
		// Reported as float32, the precision the model was stored in.
		out = append(out, Item{Item: m.items[p.item], Score: float64(float32(p.score))})
	}
	if len(out) == k {
		return out
	}
	for n, j := range m.popular {
		if len(out) == k {
			break
		}
		if int(j) == self || has(exclude, j) || slices.ContainsFunc(picked, func(p scored) bool { return p.item == j }) {
			continue
		}
		out = append(out, Item{Item: m.items[j], Score: float64(m.popularScores[n]), Reason: ReasonPopular})
	}
	return out
}

// has reports whether ascending ids hold id.
func has(ids []uint32, id uint32) bool {
	_, found := slices.BinarySearch(ids, id)
	return found
}

// topFactors scores every item by its dot product with vec, skipping the
// ascending exclude list as it walks the items in order.
func (m *Model) topFactors(vec []float32, k int, exclude []uint32) []scored {
	top := newTopK(k)
	next := 0
	for i := range m.items {
		if next < len(exclude) && exclude[next] == uint32(i) {
			next++
			continue
		}
		top.offer(uint32(i), dot(vec, m.itemFactors[i*m.factors:(i+1)*m.factors]))
	}
	return top.sorted()
}

// topNeighbors adds up, for every item, its similarity to each item the
// person has, in the order the worker adds them, and keeps the k best items
// with a positive sum.
func (m *Model) topNeighbors(seen []uint32, k int, exclude []uint32) []scored {
	sums := make(map[uint32]float64, len(seen)*m.neighbors)
	for _, j := range seen {
		for s, n := range m.neighborsOf(int(j)) {
			if n == empty {
				break
			}
			sums[n] += float64(m.nbrScores[int(j)*m.neighbors+s])
		}
	}
	top := newTopK(k)
	for i, sum := range sums {
		if sum > 0 && !has(exclude, i) {
			top.offer(i, sum)
		}
	}
	return top.sorted()
}

// topCosine is the k items whose factors are closest in angle to item's.
func (m *Model) topCosine(item, k int) []scored {
	vec := m.itemFactors[item*m.factors : (item+1)*m.factors]
	top := newTopK(k)
	for i := range m.items {
		if i == item {
			continue
		}
		cos := dot(vec, m.itemFactors[i*m.factors:(i+1)*m.factors]) / (m.itemNorms[i] * m.itemNorms[item])
		top.offer(uint32(i), cos)
	}
	return top.sorted()
}

// scored is an item index and its score.
type scored struct {
	item  uint32
	score float64
}

// better orders a before b: a higher score, then a lower item index, which is
// how the worker breaks ties.
func better(a, b scored) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	return a.item < b.item
}

// topK keeps the k best of what it is offered in a heap whose root is the
// worst kept, so an offer is O(log k) and nothing is sorted in full.
type topK struct {
	k    int
	heap []scored
}

func newTopK(k int) *topK { return &topK{k: k, heap: make([]scored, 0, k)} }

func (t *topK) offer(item uint32, score float64) {
	s := scored{item: item, score: score}
	if len(t.heap) < t.k {
		t.heap = append(t.heap, s)
		t.up(len(t.heap) - 1)
		return
	}
	if better(s, t.heap[0]) {
		t.heap[0] = s
		t.down(0)
	}
}

// up moves heap[i] towards the root while it is worse than its parent.
func (t *topK) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !better(t.heap[parent], t.heap[i]) {
			return
		}
		t.heap[parent], t.heap[i] = t.heap[i], t.heap[parent]
		i = parent
	}
}

// down moves heap[i] away from the root while a child is worse than it.
func (t *topK) down(i int) {
	n := len(t.heap)
	for {
		worst := i
		for _, c := range [2]int{2*i + 1, 2*i + 2} {
			if c < n && better(t.heap[worst], t.heap[c]) {
				worst = c
			}
		}
		if worst == i {
			return
		}
		t.heap[i], t.heap[worst] = t.heap[worst], t.heap[i]
		i = worst
	}
}

// sorted is what was kept, best first.
func (t *topK) sorted() []scored {
	out := slices.Clone(t.heap)
	slices.SortFunc(out, func(a, b scored) int {
		if better(a, b) {
			return -1
		}
		return 1
	})
	return out
}
