// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

// Package recommend serves the recommendation models the hermod-ml worker
// trains (ml/worker/hermod_ml/recommend.py) in-process: it parses a
// version's artifact once and answers "which items next for this person" and
// "which items are like this one" from it, without a call to the worker.
//
// The artifact is little-endian throughout:
//
//	"HREC"            4 bytes, magic
//	format version    uint32, FormatVersion
//	header length H   uint32
//	header            H bytes of JSON: algorithm, users, items, factors,
//	                  neighbors, seen, popular
//	user factors      float32 [users x factors]      when factors > 0
//	item factors      float32 [items x factors]      when factors > 0
//	neighbor items    uint32  [items x neighbors]    when neighbors > 0
//	neighbor scores   float32 [items x neighbors]    when neighbors > 0
//	seen offsets      uint32  [users + 1]
//	seen items        uint32  [seen], ascending per person
//	popular items     uint32  [popular], most popular first
//	popular scores    float32 [popular]
//
// Format 2 is a recommendation scenario's (ml/worker/hermod_ml/scenarios.py):
// the same sections, then what serving the scenario needs. Its header adds
// scenario, as_of, period_days, customer_neighbors, the names of the
// customer and item features, customer_attrs and item_attrs (one value or
// null per customer or item), salesmen, weights, rules, global_gap,
// global_visit_gap, has_visits and has_margin, and, since customer
// categories and sizes, has_peers and size_edges; since products' details,
// new_days and pair_details; since returns and quotes, signals. Its
// algorithm may be "none", with neither factors nor neighbors, for a
// scenario that needs no products.
// After popular scores come:
//
//	customer neighbors     uint32  [users x customer_neighbors] when > 0
//	customer nbr scores    float32 [users x customer_neighbors] when > 0
//	seen last day          uint32  [seen], days since 1970-01-01, or empty
//	seen gap               float32 [seen], median days between orders, or NaN
//	customer features      float32 [users x customer features], NaN unknown
//	item features          float32 [items x item features], NaN unknown
//	seen quantity          float32 [seen], median quantity per order, or NaN
//	                       (when pair_details)
//	seen spend             float32 [seen], what was spent on the pair
//	                       (when pair_details)
//	signal customers       uint32  [signals]                (when signals)
//	signal items           uint32  [signals]                (when signals)
//	signal days            uint32  [signals], days since 1970-01-01
//	signal kinds           uint32  [signals], 0 returned, 1 lost quote,
//	                       2 open quote                     (when signals)
//
// An artifact comes from the worker's model store, so it is parsed as input
// that may be wrong: every count is checked against the bytes that back it
// before anything is allocated for it, every index against what it indexes,
// and every float for being finite. A bad artifact is refused with ErrInvalid;
// nothing in it can make Parse or a later call panic or allocate more than the
// artifact's own size allows.
package recommend

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// Limits of an artifact. MaxArtifactBytes matches the worker's own limit: a
// scenario's artifact carries per-customer features and neighbours besides
// the model, so it is 128 MiB, twice format 1's original 64.
const (
	FormatVersion         = 1
	FormatVersionScenario = 2
	MaxArtifactBytes      = 128 << 20
	MaxFactors            = 1024
	MaxNeighbors          = 1024
	MaxPopular            = 1000
	MaxCustomerNeighbors  = 1024
	MaxFeatures           = 64
)

// The algorithms an artifact may hold. AlgorithmNone is a format 2
// scenario's that needs no products (a visit plan or reorder dates without
// a product column).
const (
	AlgorithmALS            = "als"
	AlgorithmItemSimilarity = "item_similarity"
	AlgorithmNone           = "none"
)

const (
	magic      = "HREC"
	prefixSize = 12
	// empty marks an unused neighbor slot. Used slots come first.
	empty = math.MaxUint32
)

// ErrInvalid is returned by Parse for bytes that are not a well-formed
// recommendation artifact. The error says what is wrong.
var ErrInvalid = errors.New("not a valid recommendation artifact")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, args...)...)
}

// header is the artifact's JSON header. Counts are int64 so a hostile value
// decodes and is refused, rather than failing to decode on some platforms.
type header struct {
	Algorithm string   `json:"algorithm"`
	Users     []string `json:"users"`
	Items     []string `json:"items"`
	Factors   int64    `json:"factors"`
	Neighbors int64    `json:"neighbors"`
	Seen      int64    `json:"seen"`
	Popular   int64    `json:"popular"`

	// version is the artifact's format; the rest is format 2's.
	version           uint32
	CustomerNeighbors int64                   `json:"customer_neighbors"`
	Scenario          string                  `json:"scenario"`
	AsOf              string                  `json:"as_of"`
	PeriodDays        int64                   `json:"period_days"`
	CustomerFeatures  []string                `json:"customer_features"`
	ItemFeatures      []string                `json:"item_features"`
	CustomerAttrs     map[string][]*string    `json:"customer_attrs"`
	ItemAttrs         map[string][]*string    `json:"item_attrs"`
	Salesmen          map[string]SalesmanInfo `json:"salesmen"`
	Weights           map[string]float64      `json:"weights"`
	Rules             []Rule                  `json:"rules"`
	GlobalGap         float64                 `json:"global_gap"`
	GlobalVisitGap    float64                 `json:"global_visit_gap"`
	HasVisits         bool                    `json:"has_visits"`
	HasMargin         bool                    `json:"has_margin"`
	// HasPeers is set when customers have a category (segment) or a size,
	// so products and audience add the peer share; SizeEdges are the two
	// tertiles a numeric size was cut at, none for sizes given as labels.
	// An artifact from before them has neither. NearKm and NearPeers make a
	// customer's peers the NearPeers nearest of them within NearKm
	// kilometres, when there are enough; 0 (an artifact without peers, or
	// from before nearby ones) for its whole peer group.
	HasPeers  bool      `json:"has_peers"`
	SizeEdges []float64 `json:"size_edges"`
	NearKm    float64   `json:"near_km"`
	NearPeers int       `json:"near_peers"`
	// NewDays makes a product launched within that many days new; 0 for a
	// model without launch dates. PairDetails is set when the seen quantity
	// and spend sections follow the item features: a model with products'
	// brands, pack sizes or launch dates. An artifact from before products'
	// details has neither.
	NewDays     int  `json:"new_days"`
	PairDetails bool `json:"pair_details"`
	// Signals is how many signals of returns and quotes follow the seen
	// spend, ascending by customer, item and kind; 0 for a model trained
	// without returns or quotes, or before them.
	Signals int64 `json:"signals"`
}

// SalesmanInfo is a salesman's capacity as the salesmen table gave it; a nil
// or empty field was not given.
type SalesmanInfo struct {
	VisitsPerDay *float64 `json:"visits_per_day"`
	WorkingDays  []string `json:"working_days"`
}

// Model is a parsed artifact. It is immutable, so one Model serves every
// caller at once.
type Model struct {
	algorithm string
	users     []string
	items     []string
	userIndex map[string]int
	itemIndex map[string]int

	factors     int
	userFactors []float32
	itemFactors []float32
	// itemNorms are the item factors' lengths, 1 where a length is 0, for
	// cosine similarity.
	itemNorms []float64

	neighbors int
	nbrItems  []uint32
	nbrScores []float32

	seenOff   []uint32
	seenItems []uint32

	popular       []uint32
	popularScores []float32

	// sc is what a format 2 artifact adds; nil for format 1.
	sc *scenarioData
}

// scenarioData is a format 2 artifact's scenario, features and attributes.
type scenarioData struct {
	scenario   string
	asOf       int // days since 1970-01-01
	periodDays int

	cnb        int
	cNbrs      []uint32
	cNbrScores []float32

	seenLast []uint32
	seenGap  []float32

	cFeatNames, iFeatNames []string
	cFeatIndex, iFeatIndex map[string]int
	cFeat, iFeat           []float32

	// cAttrs and iAttrs hold one value per customer or item, "" for none.
	cAttrs, iAttrs map[string][]string
	salesmen       map[string]SalesmanInfo
	weights        map[string]float64
	rules          []Rule

	globalGap, globalVisitGap float64
	hasVisits, hasMargin      bool
	hasPeers                  bool
	sizeEdges                 []float64
	nearKm                    float64
	nearPeers                 int

	// newDays, seenQty and seenSpend are products' details' (header
	// NewDays and PairDetails); 0 and nil without them.
	newDays            int
	seenQty, seenSpend []float32

	// signals are what returns and quotes say of a customer and product
	// (header Signals), ascending; nil without them.
	signals []pairSignal
}

// Algorithm is the algorithm that trained the model.
func (m *Model) Algorithm() string { return m.algorithm }

// Users is how many people the model knows.
func (m *Model) Users() int { return len(m.users) }

// Items is how many items the model can recommend: its catalogue.
func (m *Model) Items() int { return len(m.items) }

// reader takes sections off the artifact's body in order.
type reader struct {
	buf []byte
	err error
}

func (r *reader) take(n int64, what string) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || n*4 > int64(len(r.buf)) {
		r.err = invalid("%s needs %d bytes; the artifact has %d left", what, n*4, len(r.buf))
		return nil
	}
	out := r.buf[:n*4]
	r.buf = r.buf[n*4:]
	return out
}

func (r *reader) floats(n int64, what string) []float32 {
	raw := r.take(n, what)
	if r.err != nil {
		return nil
	}
	out := make([]float32, n)
	for i := range out {
		f := math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			r.err = invalid("%s holds a value that is not finite", what)
			return nil
		}
		out[i] = f
	}
	return out
}

// floatsOrNaN is floats that may hold NaN, which a format 2 section uses for
// "unknown"; an infinity is still refused.
func (r *reader) floatsOrNaN(n int64, what string) []float32 {
	raw := r.take(n, what)
	if r.err != nil {
		return nil
	}
	out := make([]float32, n)
	for i := range out {
		f := math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
		if math.IsInf(float64(f), 0) {
			r.err = invalid("%s holds an infinite value", what)
			return nil
		}
		out[i] = f
	}
	return out
}

func (r *reader) uints(n int64, what string) []uint32 {
	raw := r.take(n, what)
	if r.err != nil {
		return nil
	}
	out := make([]uint32, n)
	for i := range out {
		out[i] = binary.LittleEndian.Uint32(raw[4*i:])
	}
	return out
}

// Parse reads and checks an artifact.
func Parse(raw []byte) (*Model, error) {
	if len(raw) > MaxArtifactBytes {
		return nil, invalid("it is %d bytes, over the %d-byte limit", len(raw), MaxArtifactBytes)
	}
	if len(raw) < prefixSize || string(raw[:4]) != magic {
		return nil, invalid("it does not start with the %q magic", magic)
	}
	v := binary.LittleEndian.Uint32(raw[4:])
	if v != FormatVersion && v != FormatVersionScenario {
		return nil, invalid("artifact format %d is not format %d or %d", v, FormatVersion, FormatVersionScenario)
	}
	size := int64(binary.LittleEndian.Uint32(raw[8:]))
	if size > int64(len(raw)-prefixSize) {
		return nil, invalid("its header is %d bytes; the artifact has %d after the prefix", size, len(raw)-prefixSize)
	}
	h := header{version: v}
	if err := json.Unmarshal(raw[prefixSize:prefixSize+size], &h); err != nil {
		return nil, invalid("its header is not valid JSON: %v", err)
	}
	if err := h.check(); err != nil {
		return nil, err
	}
	m := &Model{algorithm: h.Algorithm, users: h.Users, items: h.Items, factors: int(h.Factors), neighbors: int(h.Neighbors)}
	var err error
	if m.userIndex, err = index(h.Users, "person"); err != nil {
		return nil, err
	}
	if m.itemIndex, err = index(h.Items, "item"); err != nil {
		return nil, err
	}

	if err := m.readSections(raw[prefixSize+size:], h); err != nil {
		return nil, err
	}
	if err := m.checkSections(h.Seen); err != nil {
		return nil, err
	}
	if m.sc != nil {
		if err := m.checkScenarioSections(); err != nil {
			return nil, err
		}
	}
	m.normalize()
	return m, nil
}

// readSections reads the sections after the header, which must fill buf
// exactly.
func (m *Model) readSections(buf []byte, h header) error {
	nUsers, nItems := int64(len(h.Users)), int64(len(h.Items))
	r := &reader{buf: buf}
	if h.Factors > 0 {
		m.userFactors = r.floats(nUsers*h.Factors, "user factors")
		m.itemFactors = r.floats(nItems*h.Factors, "item factors")
	}
	if h.Neighbors > 0 {
		m.nbrItems = r.uints(nItems*h.Neighbors, "neighbor items")
		m.nbrScores = r.floats(nItems*h.Neighbors, "neighbor scores")
	}
	m.seenOff = r.uints(nUsers+1, "seen offsets")
	m.seenItems = r.uints(h.Seen, "seen items")
	m.popular = r.uints(h.Popular, "popular items")
	m.popularScores = r.floats(h.Popular, "popular scores")
	if h.version == FormatVersionScenario {
		m.readScenario(r, h)
	}
	if r.err != nil {
		return r.err
	}
	if len(r.buf) != 0 {
		return invalid("%d bytes follow its last section", len(r.buf))
	}
	return nil
}

// normalize keeps each item factor vector's norm, for similar items by
// cosine; a zero vector counts as norm 1.
func (m *Model) normalize() {
	if m.factors == 0 {
		return
	}
	m.itemNorms = make([]float64, len(m.items))
	for i := range m.items {
		v := m.itemFactors[i*m.factors : (i+1)*m.factors]
		if n := math.Sqrt(dot(v, v)); n > 0 {
			m.itemNorms[i] = n
		} else {
			m.itemNorms[i] = 1
		}
	}
}

// check bounds the header's counts. The ids' count is bounded by the header's
// own size; the section counts are bounded again by the bytes in take.
func (h header) check() error {
	none := h.version == FormatVersionScenario && h.Algorithm == AlgorithmNone
	switch {
	case h.Algorithm != AlgorithmALS && h.Algorithm != AlgorithmItemSimilarity && !none:
		return invalid("algorithm %q is not %q or %q", h.Algorithm, AlgorithmALS, AlgorithmItemSimilarity)
	case len(h.Users) == 0:
		return invalid("it knows no people")
	case len(h.Items) == 0 && !none:
		return invalid("it has no items")
	case h.Factors < 0 || h.Factors > MaxFactors:
		return invalid("factors %d is not between 0 and %d", h.Factors, MaxFactors)
	case h.Neighbors < 0 || h.Neighbors > MaxNeighbors:
		return invalid("neighbors %d is not between 0 and %d", h.Neighbors, MaxNeighbors)
	case none && (h.Factors > 0 || h.Neighbors > 0):
		return invalid("algorithm none holds neither factors nor neighbors")
	case !none && ((h.Factors > 0) == (h.Neighbors > 0) || (h.Factors > 0) != (h.Algorithm == AlgorithmALS)):
		return invalid("it must hold exactly one of factors (als) and neighbors (item_similarity)")
	case h.Seen < 0 || h.Seen > MaxArtifactBytes/4:
		return invalid("seen %d is out of range", h.Seen)
	case h.Popular < 0 || h.Popular > int64(len(h.Items)) || h.Popular > MaxPopular:
		return invalid("popular %d is not between 0 and its %d items (at most %d)", h.Popular, len(h.Items), MaxPopular)
	}
	if h.version == FormatVersionScenario {
		return h.checkScenario()
	}
	return nil
}

func index(ids []string, what string) (map[string]int, error) {
	out := make(map[string]int, len(ids))
	for i, id := range ids {
		if id == "" {
			return nil, invalid("a %s id is empty", what)
		}
		if _, dup := out[id]; dup {
			return nil, invalid("%s %q appears twice", what, id)
		}
		out[id] = i
	}
	return out, nil
}

// checkSections checks every index against what it indexes.
func (m *Model) checkSections(seen int64) error {
	if err := m.checkNeighbors(); err != nil {
		return err
	}
	if err := m.checkSeen(seen); err != nil {
		return err
	}
	return m.checkPopular()
}

// checkNeighbors checks each item's neighbor slots: items of the model, then
// only empty slots.
func (m *Model) checkNeighbors() error {
	nItems := uint32(len(m.items))
	for i := range m.items {
		slots := m.neighborsOf(i)
		ended := false
		for _, j := range slots {
			switch {
			case j == empty:
				ended = true
			case ended:
				return invalid("item %q has a neighbor after an empty slot", m.items[i])
			case j >= nItems:
				return invalid("item %q has neighbor %d of %d items", m.items[i], j, nItems)
			}
		}
	}
	return nil
}

// checkSeen checks the seen offsets run from 0 to seen without going back,
// and each person's seen items are items of the model, strictly ascending.
func (m *Model) checkSeen(seen int64) error {
	nItems := uint32(len(m.items))
	if m.seenOff[0] != 0 || int64(m.seenOff[len(m.seenOff)-1]) != seen {
		return invalid("seen offsets must run from 0 to %d", seen)
	}
	for u := range m.users {
		lo, hi := m.seenOff[u], m.seenOff[u+1]
		if hi < lo || int64(hi) > seen {
			return invalid("seen offsets go back or past %d at person %q", seen, m.users[u])
		}
		for k := lo; k < hi; k++ {
			if m.seenItems[k] >= nItems {
				return invalid("person %q has seen item %d of %d items", m.users[u], m.seenItems[k], nItems)
			}
			if k > lo && m.seenItems[k] <= m.seenItems[k-1] {
				return invalid("person %q's seen items are not strictly ascending", m.users[u])
			}
		}
	}
	return nil
}

// checkPopular checks the popular items are distinct items of the model.
func (m *Model) checkPopular() error {
	nItems := uint32(len(m.items))
	taken := make(map[uint32]bool, len(m.popular))
	for _, j := range m.popular {
		if j >= nItems {
			return invalid("popular item %d of %d items", j, nItems)
		}
		if taken[j] {
			return invalid("popular item %q appears twice", m.items[j])
		}
		taken[j] = true
	}
	return nil
}

// neighborsOf is an item's neighbor slots; nil for a factors model.
func (m *Model) neighborsOf(i int) []uint32 {
	if m.neighbors == 0 {
		return nil
	}
	return m.nbrItems[i*m.neighbors : (i+1)*m.neighbors]
}

// seenBy is a person's seen items, ascending.
func (m *Model) seenBy(u int) []uint32 {
	return m.seenItems[m.seenOff[u]:m.seenOff[u+1]]
}

// dot is the float64 dot product of two float32 vectors of one length.
func dot(a, b []float32) float64 {
	sum := 0.0
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}
