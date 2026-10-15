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
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
)

// Products' details (the worker's _details): a product's brand, subcategory,
// pack size and unit, launch and end days, stock on hand and substitute. A
// model trained before them, or without them, answers as it did.

// NewDaysMin and NewDaysMax bound a model's new_days, as the worker's
// NEW_DAYS_BOUNDS; MaxStock bounds the stock a request may give.
const (
	NewDaysMin = 1
	NewDaysMax = 365
	MaxStock   = MaxBatch * 10
)

// The reason codes of products' details.
const (
	ReasonBrand      = "brand"
	ReasonNewProduct = "new_product"
	ReasonSubstitute = "substitute"
	ReasonSameKind   = "same_kind"
)

// As the worker's BRAND_REASON, BRAND_CANDIDATES, NEW_CANDIDATES,
// AUDIENCE_NEW, SAME_SUBCATEGORY, SAME_BRAND and SIMILAR_KIN: the brand
// share of spend that makes "Buys a lot of <brand>" a reason, how many of
// its brands' and of new products a customer's products also take, a new
// product's weight in an audience, what sharing a subcategory and a brand
// add to a similar product, and how many products a similar list takes for
// sharing them alone.
const (
	brandReason     = 0.2
	brandCandidates = 10
	newCandidates   = 10
	audienceNew     = 0.3
	sameSubcategory = 0.1
	sameBrand       = 0.05
	similarKin      = 20
)

// The item features of products' details, and the one candidates of a
// brand are ranked by.
const (
	featPackSize  = "pack_size"
	featLaunchDay = "launch_day"
	featEndDay    = "end_day"
	featStock     = "stock"
	featBuyers    = "buyers"
)

// The reasons a product cannot be offered.
const (
	whyDiscontinued = "discontinued"
	whyOutOfStock   = "out of stock"
)

// itemDetail is item i's feature, NaN for a format 1 model or one without it.
func (m *Model) itemDetail(i int, name string) float64 {
	if m.sc == nil {
		return math.NaN()
	}
	return m.itf(i, name)
}

// stock is item i's stock on hand: the request's, when it gives one, else
// the model's; NaN when neither knows.
func (c *call) stock(i int) float64 {
	if s, ok := c.q.Stock[c.m.items[i]]; ok {
		return s
	}
	return c.m.itemDetail(i, featStock)
}

// unavailable says why item i cannot be offered today, as the worker's
// unavailable: "discontinued" after its end day, "out of stock" with no
// stock on hand; "" when it can be.
func (c *call) unavailable(i int) string {
	if end := c.m.itemDetail(i, featEndDay); finite(end) && end < float64(c.today) {
		return whyDiscontinued
	}
	if s := c.stock(i); finite(s) && s <= 0 {
		return whyOutOfStock
	}
	return ""
}

// substituteOf is item i's substitute, when the model knows it and it can
// be offered.
func (c *call) substituteOf(i int) (int, bool) {
	sub := c.m.iattr(i, "substitute")
	if sub == "" {
		return 0, false
	}
	j, ok := c.m.itemIndex[sub]
	if !ok || j == i || c.unavailable(j) != "" {
		return 0, false
	}
	return j, true
}

// offerable is cands, best first, without what cannot be offered, as the
// worker's offerable: an item out of stock or past its end day gives way to
// its substitute, at its place and with its score (the substitute's own
// later place is dropped), unless the substitute is listed before it or is
// in mine (ascending); otherwise it is left out.
func (c *call) offerable(cands []cand, mine []uint32) []cand {
	placed := map[int]bool{}
	out := make([]cand, 0, len(cands))
	for _, cd := range cands {
		if placed[cd.idx] {
			continue
		}
		why := c.unavailable(cd.idx)
		if why == "" {
			out = append(out, cd)
			placed[cd.idx] = true
			continue
		}
		j, ok := c.substituteOf(cd.idx)
		if !ok || placed[j] || has(mine, uint32(j)) {
			continue
		}
		placed[j] = true
		out = append(out, cand{idx: j, score: cd.score, reasons: []Reason{{Code: ReasonSubstitute,
			Text: fmt.Sprintf("Instead of %s (%s)", c.itemName(cd.idx), why)}}})
	}
	return out
}

// itemName is item i's name, or its id.
func (c *call) itemName(i int) string {
	if n := c.m.iattr(i, "name"); n != "" {
		return n
	}
	return c.m.items[i]
}

// launched is item i's launch day when it is new, as the worker's launched:
// launched within the model's newDays days before today.
func (c *call) launched(i int) (float64, bool) {
	if c.m.sc == nil || c.m.sc.newDays == 0 {
		return 0, false
	}
	launch := c.m.itf(i, featLaunchDay)
	if !finite(launch) {
		return 0, false
	}
	age := float64(c.today) - launch
	return launch, age >= 0 && age < float64(c.m.sc.newDays)
}

// spendShares is customer u's share of spend on each value of an item
// attribute (brand, subcategory, category), of all it spent, as the
// worker's spend_shares; nil for a model without that attribute or its
// spend, or a customer who spent nothing.
func (c *call) spendShares(u int, key string) map[string]float64 {
	m := c.m
	if m.sc == nil || m.sc.seenSpend == nil {
		return nil
	}
	values, ok := m.sc.iAttrs[key]
	if !ok {
		return nil
	}
	start := int(m.seenOff[u])
	total := 0.0
	by := map[string]float64{}
	for k, i := range m.seenBy(u) {
		s := float64(m.sc.seenSpend[start+k])
		total += s
		if v := values[i]; v != "" {
			by[v] += s
		}
	}
	if total <= 0 {
		return nil
	}
	for v, s := range by {
		by[v] = s / total
	}
	return by
}

// groupKey is the attribute a new product's likely buyers are found by: its
// subcategory, or its category for a model without subcategories.
func (m *Model) groupKey() string {
	if _, ok := m.sc.iAttrs["subcategory"]; ok {
		return "subcategory"
	}
	return "category"
}

// fit is how much a customer buys of item i's kind, as the worker's _fit:
// its share of spend on i's brand or on i's subcategory, whichever is
// larger.
func (c *call) fit(i int, brands, groups map[string]float64) float64 {
	out := 0.0
	if b := c.m.iattr(i, "brand"); b != "" {
		out = brands[b]
	}
	if g := c.m.iattr(i, c.m.groupKey()); g != "" {
		out = max(out, groups[g])
	}
	return out
}

func newReason(launch float64) Reason {
	return Reason{Code: ReasonNewProduct, Text: "New, launched " + dayText(int(launch))}
}

func brandReasonOf(brand string, share float64) Reason {
	return Reason{Code: ReasonBrand, Text: fmt.Sprintf("Buys a lot of %s (%d%% of spend)", brand, round(share*100))}
}

// brandCandidatesOf are the products of the brands a customer spends at
// least brandReason on, its biggest brand first, each brand's most bought
// first, not taken (which it adds them to); brandCandidates at most.
func (c *call) brandCandidatesOf(brands map[string]float64, taken map[int]bool) []candidate {
	m := c.m
	type share struct {
		brand string
		share float64
	}
	var top []share
	for b, s := range brands {
		if s >= brandReason {
			top = append(top, share{b, s})
		}
	}
	slices.SortFunc(top, func(a, b share) int {
		if a.share != b.share {
			return cmp.Compare(b.share, a.share)
		}
		return strings.Compare(a.brand, b.brand)
	})
	var out []candidate
	for _, t := range top {
		var items []int
		for i := range m.items {
			if m.iattr(i, "brand") == t.brand && !taken[i] {
				items = append(items, i)
			}
		}
		slices.SortFunc(items, func(a, b int) int {
			if x, y := m.itf(a, featBuyers), m.itf(b, featBuyers); x != y {
				return cmp.Compare(y, x)
			}
			return a - b
		})
		for _, i := range items {
			if len(out) == brandCandidates {
				return out
			}
			out = append(out, candidate{item: i, reason: ReasonBrand})
			taken[i] = true
		}
	}
	return out
}

// newCandidatesOf are the new products of a kind the customer buys, not
// taken, latest launch first; newCandidates at most.
func (c *call) newCandidatesOf(brands, groups map[string]float64, taken map[int]bool) []candidate {
	type launch struct {
		day  float64
		item int
	}
	var list []launch
	for i := range c.m.items {
		if taken[i] {
			continue
		}
		if day, ok := c.launched(i); ok && c.fit(i, brands, groups) > 0 {
			list = append(list, launch{day, i})
		}
	}
	slices.SortFunc(list, func(a, b launch) int {
		if a.day != b.day {
			return cmp.Compare(b.day, a.day)
		}
		return a.item - b.item
	})
	out := make([]candidate, 0, min(len(list), newCandidates))
	for _, l := range list[:min(len(list), newCandidates)] {
		out = append(out, candidate{item: l.item, reason: reasonNew})
	}
	return out
}

// reasonNew marks a candidate taken for being a new product of the
// customer's kind.
const reasonNew = "new"

// pairQuantity is customer u's median quantity per order of item i; ok is
// false when it never bought it or the model has no quantities.
func (m *Model) pairQuantity(u, i int) (float64, bool) {
	if m.sc == nil || m.sc.seenQty == nil {
		return 0, false
	}
	k, found := slices.BinarySearch(m.seenBy(u), uint32(i))
	if !found {
		return 0, false
	}
	q := float64(m.sc.seenQty[int(m.seenOff[u])+k])
	return q, finite(q) && q > 0
}

// suggestion is what customer u might order of item i, from its median
// quantity per order rounded up, as the worker's suggestion: in whole packs
// when the item has a pack size ("3 cartons (100 pcs)"), otherwise in
// pieces or the item's unit; nil for a model without pack sizes or a pair
// without a quantity.
func (m *Model) suggestion(u, i int) map[string]any {
	if _, ok := m.sc.iFeatIndex[featPackSize]; !ok {
		return nil
	}
	qty, ok := m.pairQuantity(u, i)
	if !ok {
		return nil
	}
	need := int(math.Ceil(qty))
	unit := m.iattr(i, "unit")
	if pack := m.itf(i, featPackSize); finite(pack) && pack > 0 {
		packs := int(math.Ceil(float64(need) / pack))
		if unit == "" {
			unit = "pack"
		}
		return map[string]any{"suggested_qty": packs, "suggested_text": fmt.Sprintf("%d %s (%d pcs)", packs, plural(unit, packs), need)}
	}
	if unit == "" {
		unit = "pcs"
	}
	return map[string]any{"suggested_qty": need, "suggested_text": fmt.Sprintf("%d %s", need, unit)}
}

func plural(unit string, n int) string {
	if n == 1 || strings.HasSuffix(unit, "s") {
		return unit
	}
	return unit + "s"
}

// suggest adds to each product pick what customer u might order of it.
func (c *call) suggest(u int, picks []Pick) {
	if c.m.sc == nil {
		return
	}
	for _, p := range picks {
		for k, v := range c.m.suggestion(u, c.m.itemIndex[p.ID]) {
			p.Fields[k] = v
		}
	}
}

// checkKind reads a request's brand and subcategory: they keep the products
// of a product list, so they are refused for customer lists, and for a
// model that does not know them.
func (m *Model) checkKind(scenario string, q *Query) error {
	q.Brand, q.Subcategory = strings.TrimSpace(q.Brand), strings.TrimSpace(q.Subcategory)
	if q.Brand == "" && q.Subcategory == "" {
		return nil
	}
	if itemEntity(scenario) != entityProducts {
		return badQuery(`"brand" and "subcategory" keep the products of product lists (products, similar), not %s results`, scenario)
	}
	for _, f := range []struct{ attr, value string }{{"brand", q.Brand}, {"subcategory", q.Subcategory}} {
		if f.value == "" {
			continue
		}
		if m.sc == nil || m.sc.iAttrs[f.attr] == nil {
			return badQuery("this model was trained without a product %s (products.%s): train it again with one to keep only some products", f.attr, f.attr)
		}
	}
	return nil
}

// checkStock refuses a request's stock that is not a number of items.
func checkStock(stock map[string]float64) error {
	if len(stock) > MaxStock {
		return badQuery(`at most %d products' "stock" per call, not %d`, MaxStock, len(stock))
	}
	for id, s := range stock {
		if id == "" || !finite(s) {
			return badQuery(`"stock" maps product ids to numbers of items on hand`)
		}
	}
	return nil
}

// inKind reports whether product i is of the request's brand and
// subcategory.
func (c *call) inKind(i int) bool {
	q := c.q
	return (q.Brand == "" || strings.EqualFold(c.m.iattr(i, "brand"), q.Brand)) &&
		(q.Subcategory == "" || strings.EqualFold(c.m.iattr(i, "subcategory"), q.Subcategory))
}

// kinOf is what product j shares with product i: its similar-list bonus
// and why, as the worker's similar_scores' kin; 0 and no reason for
// nothing.
func (m *Model) kinOf(i, j int) (float64, *Reason) {
	sub, brand := m.iattr(i, "subcategory"), m.iattr(i, "brand")
	sameSub := sub != "" && m.iattr(j, "subcategory") == sub
	sameBr := brand != "" && m.iattr(j, "brand") == brand
	bonus := 0.0
	if sameSub {
		bonus += sameSubcategory
	}
	if sameBr {
		bonus += sameBrand
	}
	switch {
	case sameSub && sameBr:
		return bonus, &Reason{Code: ReasonSameKind, Text: fmt.Sprintf("Same brand and subcategory (%s, %s)", brand, sub)}
	case sameSub:
		return bonus, &Reason{Code: ReasonSameKind, Text: fmt.Sprintf("Same subcategory (%s)", sub)}
	case sameBr:
		return bonus, &Reason{Code: ReasonSameKind, Text: fmt.Sprintf("Same brand (%s)", brand)}
	}
	return 0, nil
}

// similarKinCands re-ranks a product's similar items by what they share with
// it, as the worker's similar_scores: a neighbour of its subcategory gains
// sameSubcategory and of its brand sameBrand, the similarKin products that
// share most with it join them on that alone, and the popular fill comes
// after them.
func (c *call) similarKinCands(i int, items []Item, together Reason) []cand {
	m := c.m
	var ranked, fill []cand
	listed := map[int]bool{i: true}
	for _, it := range items {
		if it.Reason == "" {
			listed[m.itemIndex[it.Item]] = true
		}
	}
	for _, it := range items {
		j := m.itemIndex[it.Item]
		if it.Reason == ReasonPopular {
			continue
		}
		bonus, r := m.kinOf(i, j)
		reasons := []Reason{together}
		if r != nil {
			reasons = append(reasons, *r)
		}
		ranked = append(ranked, cand{idx: j, score: float64(float32(it.Score + bonus)), reasons: reasons})
	}
	for _, e := range m.kinOnly(i, listed) {
		_, r := m.kinOf(i, e.j)
		ranked = append(ranked, cand{idx: e.j, score: float64(float32(e.bonus)), reasons: []Reason{*r}})
		listed[e.j] = true
	}
	slices.SortStableFunc(ranked, byScore)
	for _, it := range items {
		if j := m.itemIndex[it.Item]; it.Reason == ReasonPopular && !listed[j] {
			fill = append(fill, cand{idx: j, score: it.Score, reasons: []Reason{popularReason}})
		}
	}
	return append(ranked, fill...)
}

// kin is a product sharing a subcategory or brand with another, and what
// that adds to its similar-list score.
type kin struct {
	bonus float64
	j     int
}

// kinOnly are the similarKin products not listed that share most with
// product i, most first, then by index.
func (m *Model) kinOnly(i int, listed map[int]bool) []kin {
	var extra []kin
	for j := range m.items {
		if listed[j] {
			continue
		}
		if bonus, _ := m.kinOf(i, j); bonus > 0 {
			extra = append(extra, kin{bonus, j})
		}
	}
	slices.SortFunc(extra, func(a, b kin) int {
		if a.bonus != b.bonus {
			return cmp.Compare(b.bonus, a.bonus)
		}
		return a.j - b.j
	})
	return extra[:min(len(extra), similarKin)]
}
