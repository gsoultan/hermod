// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package recommend

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The scenarios a format 2 artifact answers. A format 1 artifact answers
// ScenarioProducts and ScenarioSimilar.
const (
	ScenarioVisits   = "visits"
	ScenarioProducts = "products"
	ScenarioReorder  = "reorder"
	ScenarioAudience = "audience"
	ScenarioSimilar  = "similar"
)

// Scenarios lists every scenario, in the order the worker documents them.
var Scenarios = []string{ScenarioVisits, ScenarioProducts, ScenarioReorder, ScenarioAudience, ScenarioSimilar}

// The visit signals, which the weights name.
const (
	SignalReorderDue  = "reorder_due"
	SignalChurnRisk   = "churn_risk"
	SignalValue       = "value"
	SignalOpportunity = "opportunity"
	SignalNeglect     = "neglect"
)

var signals = []string{SignalReorderDue, SignalChurnRisk, SignalValue, SignalOpportunity, SignalNeglect}

// DefaultWeights are a visit plan's weights when its training names none.
var DefaultWeights = map[string]float64{
	SignalReorderDue: 0.35, SignalChurnRisk: 0.2, SignalValue: 0.2, SignalOpportunity: 0.1, SignalNeglect: 0.15,
}

// The features a format 2 artifact holds per customer and per item. Only the
// ones scoring reads are named here; rules may name any the artifact lists.
const (
	featLastOrder     = "last_order"
	featOrders        = "orders"
	featMonetary      = "monetary"
	featValuePct      = "value_pct"
	featMedianGap     = "median_gap"
	featLastVisit     = "last_visit"
	featVisitGap      = "visit_gap"
	featWhitespace    = "whitespace"
	featWhitespacePct = "whitespace_pct"
	featLat           = "lat"
	featLon           = "lon"
	featMarginPct     = "margin_pct"
)

// epoch is day 0 of the artifact's day numbers.
var epoch = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

// dayOf is t's day number, days since 1970-01-01 in UTC.
func dayOf(t time.Time) int {
	return int(t.UTC().Truncate(24*time.Hour).Sub(epoch) / (24 * time.Hour))
}

// dayText is a day number as YYYY-MM-DD.
func dayText(day int) string {
	return epoch.AddDate(0, 0, day).Format(time.DateOnly)
}

// Scenario is the question the model answers: one of Scenarios. A format 1
// model answers ScenarioProducts (and similar items).
func (m *Model) Scenario() string {
	if m.sc == nil {
		return ScenarioProducts
	}
	return m.sc.scenario
}

// AsOf is the day of the latest transaction the model learned from; zero
// for a format 1 model.
func (m *Model) AsOf() time.Time {
	if m.sc == nil {
		return time.Time{}
	}
	return epoch.AddDate(0, 0, m.sc.asOf)
}

// PeriodDays is the model's planning or evaluation period in days; 7 for a
// format 1 model.
func (m *Model) PeriodDays() int {
	if m.sc == nil || m.sc.periodDays <= 0 {
		return 7
	}
	return m.sc.periodDays
}

// DefaultRules are the rules trained with the model, which every call obeys.
func (m *Model) DefaultRules() []Rule {
	if m.sc == nil {
		return nil
	}
	return slices.Clone(m.sc.rules)
}

// Salesmen lists the salesmen the model knows, from its salesmen table and
// its customers' salesman column, sorted.
func (m *Model) Salesmen() []string {
	if m.sc == nil {
		return nil
	}
	set := map[string]bool{}
	for id := range m.sc.salesmen {
		set[id] = true
	}
	for _, s := range m.sc.cAttrs["salesman"] {
		if s != "" {
			set[s] = true
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// sizeLabels are the buckets a numeric customer size is cut into, smallest
// first, as the worker's SIZE_LABELS.
var sizeLabels = []string{"small", "medium", "large"}

// SizeEdges are the two tertiles the model's numeric customer sizes were cut
// at into small, medium and large; ok is false when its sizes were labels,
// or it has none.
func (m *Model) SizeEdges() (lo, hi float64, ok bool) {
	if m.sc == nil || len(m.sc.sizeEdges) != 2 {
		return 0, 0, false
	}
	return m.sc.sizeEdges[0], m.sc.sizeEdges[1], true
}

// SizeBucket is a customer size as the model stores it: small, medium or
// large in lower case; a number cut at SizeEdges as training cut the
// customers' sizes (up to the first edge small, up to the second medium,
// above it large); any other label as given, trimmed.
func (m *Model) SizeBucket(size string) string {
	size = strings.TrimSpace(size)
	if lower := strings.ToLower(size); slices.Contains(sizeLabels, lower) {
		return lower
	}
	lo, hi, ok := m.SizeEdges()
	f, err := strconv.ParseFloat(size, 64)
	if !ok || err != nil || !finite(f) {
		return size
	}
	switch {
	case f <= lo:
		return sizeLabels[0]
	case f <= hi:
		return sizeLabels[1]
	}
	return sizeLabels[2]
}

// CustomerAttrs is a customer's category (segment) and size; "" for what
// the model does not know, or a customer it does not.
func (m *Model) CustomerAttrs(id string) (segment, size string) {
	u, ok := m.userIndex[id]
	if !ok || m.sc == nil {
		return "", ""
	}
	return m.cattr(u, "segment"), m.cattr(u, "size")
}

// CustomerMarket is a customer's price tier, channel and region; "" for
// what the model does not know, or a customer it does not.
func (m *Model) CustomerMarket(id string) (priceTier, channel, region string) {
	u, ok := m.userIndex[id]
	if !ok || m.sc == nil {
		return "", "", ""
	}
	return m.cattr(u, attrPriceTier), m.cattr(u, attrChannel), m.cattr(u, attrRegion)
}

// ProductAttrs is a product's brand and subcategory; "" for what the model
// does not know, or a product it does not.
func (m *Model) ProductAttrs(id string) (brand, subcategory string) {
	i, ok := m.itemIndex[id]
	if !ok {
		return "", ""
	}
	return m.iattr(i, "brand"), m.iattr(i, "subcategory")
}

// Customer and product ids, for building every result of a model.
func (m *Model) CustomerIDs() []string { return slices.Clone(m.users) }
func (m *Model) ProductIDs() []string  { return slices.Clone(m.items) }

// checkScenario checks a format 2 header's own fields.
func (h header) checkScenario() error {
	if !slices.Contains(Scenarios, h.Scenario) {
		return invalid("scenario %q is not one of %v", h.Scenario, Scenarios)
	}
	if h.CustomerNeighbors < 0 || h.CustomerNeighbors > MaxCustomerNeighbors {
		return invalid("customer_neighbors %d is not between 0 and %d", h.CustomerNeighbors, MaxCustomerNeighbors)
	}
	if _, err := time.Parse(time.DateOnly, h.AsOf); err != nil {
		return invalid("as_of %q is not a date", h.AsOf)
	}
	if h.PeriodDays < 0 || h.PeriodDays > 366 {
		return invalid("period_days %d is not between 0 and 366", h.PeriodDays)
	}
	if err := h.checkTables(); err != nil {
		return err
	}
	if err := h.checkSalesmen(); err != nil {
		return err
	}
	if err := h.checkNumbers(); err != nil {
		return err
	}
	if err := ValidateRules(h.Scenario, h.Rules); err != nil {
		return invalid("its rules: %v", err)
	}
	return nil
}

// checkNumbers checks the visit weights and the global gaps.
func (h header) checkNumbers() error {
	for k, w := range h.Weights {
		if !slices.Contains(signals, k) || math.IsNaN(w) || w < 0 || w > 1 {
			return invalid("weight %q = %v is not a signal's weight between 0 and 1", k, w)
		}
	}
	for _, g := range []float64{h.GlobalGap, h.GlobalVisitGap} {
		if math.IsNaN(g) || math.IsInf(g, 0) || g < 0 {
			return invalid("a global gap of %v days", g)
		}
	}
	if e := h.SizeEdges; len(e) != 0 && (len(e) != 2 || !finite(e[0]) || !finite(e[1]) || e[0] > e[1]) {
		return invalid("size_edges %v are not two ascending numbers", e)
	}
	if h.Signals < 0 || h.Signals > MaxArtifactBytes/16 {
		return invalid("signals %d is out of range", h.Signals)
	}
	if h.NewDays != 0 && (h.NewDays < NewDaysMin || h.NewDays > NewDaysMax) {
		return invalid("new_days %d is not between %d and %d", h.NewDays, NewDaysMin, NewDaysMax)
	}
	if h.NearPeers != 0 || h.NearKm != 0 {
		if !finite(h.NearKm) || h.NearKm < NearKmMin || h.NearKm > NearKmMax || h.NearPeers < NearPeersMin || h.NearPeers > NearPeersMax {
			return invalid("near_km %v and near_peers %d are not between %v and %v km, and %d and %d customers",
				h.NearKm, h.NearPeers, NearKmMin, NearKmMax, NearPeersMin, NearPeersMax)
		}
	}
	return nil
}

// checkTables checks the feature names and that every attribute has a value
// per customer or item.
func (h header) checkTables() error {
	for what, names := range map[string][]string{"customer": h.CustomerFeatures, "item": h.ItemFeatures} {
		if len(names) > MaxFeatures {
			return invalid("%d %s features, over %d", len(names), what, MaxFeatures)
		}
		if _, err := index(names, what+" feature"); err != nil {
			return err
		}
	}
	for name, values := range h.CustomerAttrs {
		if len(values) != len(h.Users) {
			return invalid("customer attribute %q has %d values for %d customers", name, len(values), len(h.Users))
		}
	}
	for name, values := range h.ItemAttrs {
		if len(values) != len(h.Items) {
			return invalid("item attribute %q has %d values for %d items", name, len(values), len(h.Items))
		}
	}
	return nil
}

// checkSalesmen checks the salesmen table.
func (h header) checkSalesmen() error {
	for id, s := range h.Salesmen {
		if id == "" {
			return invalid("a salesman id is empty")
		}
		if s.VisitsPerDay != nil && !(*s.VisitsPerDay > 0 && *s.VisitsPerDay <= MaxPlan) {
			return invalid("salesman %q's visits per day is not between 1 and %d", id, MaxPlan)
		}
		if err := checkWeekdays(s.WorkingDays); err != nil {
			return invalid("salesman %q: %v", id, err)
		}
	}
	return nil
}

// readScenario reads a format 2 artifact's sections after the popular
// scores.
func (m *Model) readScenario(r *reader, h header) {
	nUsers, nItems := int64(len(h.Users)), int64(len(h.Items))
	sc := &scenarioData{
		scenario: h.Scenario, periodDays: int(h.PeriodDays), cnb: int(h.CustomerNeighbors),
		cFeatNames: h.CustomerFeatures, iFeatNames: h.ItemFeatures,
		salesmen: h.Salesmen, weights: h.Weights, rules: h.Rules,
		globalGap: h.GlobalGap, globalVisitGap: h.GlobalVisitGap, hasVisits: h.HasVisits, hasMargin: h.HasMargin,
		hasPeers: h.HasPeers, sizeEdges: h.SizeEdges, nearKm: h.NearKm, nearPeers: h.NearPeers, newDays: h.NewDays,
	}
	asOf, _ := time.Parse(time.DateOnly, h.AsOf)
	sc.asOf = dayOf(asOf)
	sc.cFeatIndex, _ = index(h.CustomerFeatures, "customer feature")
	sc.iFeatIndex, _ = index(h.ItemFeatures, "item feature")
	sc.cAttrs, sc.iAttrs = flatten(h.CustomerAttrs), flatten(h.ItemAttrs)
	if sc.cnb > 0 {
		sc.cNbrs = r.uints(nUsers*h.CustomerNeighbors, "customer neighbors")
		sc.cNbrScores = r.floats(nUsers*h.CustomerNeighbors, "customer neighbor scores")
	}
	sc.seenLast = r.uints(h.Seen, "seen last days")
	sc.seenGap = r.floatsOrNaN(h.Seen, "seen gaps")
	sc.cFeat = r.floatsOrNaN(nUsers*int64(len(h.CustomerFeatures)), "customer features")
	sc.iFeat = r.floatsOrNaN(nItems*int64(len(h.ItemFeatures)), "item features")
	if h.PairDetails {
		sc.seenQty = r.floatsOrNaN(h.Seen, "seen quantities")
		sc.seenSpend = r.floats(h.Seen, "seen spend")
	}
	if h.Signals > 0 {
		sc.signals = readSignals(r, h.Signals)
	}
	m.sc = sc
}

// flatten turns nullable attribute values into strings, "" for null.
func flatten(attrs map[string][]*string) map[string][]string {
	out := make(map[string][]string, len(attrs))
	for name, values := range attrs {
		flat := make([]string, len(values))
		for i, v := range values {
			if v != nil {
				flat[i] = *v
			}
		}
		out[name] = flat
	}
	return out
}

// checkScenarioSections checks the customer neighbour lists, as
// checkNeighbors does items', and that gaps are not negative.
func (m *Model) checkScenarioSections() error {
	sc := m.sc
	nUsers := uint32(len(m.users))
	for u := range m.users {
		ended := false
		for _, n := range m.customerNeighborsOf(u) {
			switch {
			case n == empty:
				ended = true
			case ended:
				return invalid("customer %q has a neighbour after an empty slot", m.users[u])
			case n >= nUsers:
				return invalid("customer %q has neighbour %d of %d customers", m.users[u], n, nUsers)
			}
		}
	}
	for _, g := range sc.seenGap {
		if g < 0 {
			return invalid("a seen gap is negative")
		}
	}
	for _, s := range sc.seenSpend {
		if s < 0 {
			return invalid("a seen spend is negative")
		}
	}
	return m.checkSignals()
}

// customerNeighborsOf is a customer's similar-customer slots.
func (m *Model) customerNeighborsOf(u int) []uint32 {
	if m.sc == nil || m.sc.cnb == 0 {
		return nil
	}
	return m.sc.cNbrs[u*m.sc.cnb : (u+1)*m.sc.cnb]
}

// cf is customer u's feature, NaN when the artifact does not have it.
func (m *Model) cf(u int, name string) float64 {
	i, ok := m.sc.cFeatIndex[name]
	if !ok {
		return math.NaN()
	}
	return float64(m.sc.cFeat[u*len(m.sc.cFeatNames)+i])
}

// itf is item i's feature, NaN when the artifact does not have it.
func (m *Model) itf(i int, name string) float64 {
	j, ok := m.sc.iFeatIndex[name]
	if !ok {
		return math.NaN()
	}
	return float64(m.sc.iFeat[i*len(m.sc.iFeatNames)+j])
}

// cattr is customer u's attribute, "" for none.
func (m *Model) cattr(u int, name string) string {
	if v, ok := m.sc.cAttrs[name]; ok {
		return v[u]
	}
	return ""
}

// iattr is item i's attribute, "" for none.
func (m *Model) iattr(i int, name string) string {
	if m.sc == nil {
		return ""
	}
	if v, ok := m.sc.iAttrs[name]; ok {
		return v[i]
	}
	return ""
}
