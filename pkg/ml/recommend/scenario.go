package recommend

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Bounds of a scenario call.
const (
	// MaxPlan bounds a visit plan: its items, and visits per day.
	MaxPlan = 500
	// MaxBatch bounds the customers one call may ask for.
	MaxBatch = 1000
	// DefaultVisitsPerDay is a salesman's capacity when nothing says.
	DefaultVisitsPerDay = 8
	// candidates is how many collaborative-filtering candidates a customer's
	// products are re-ranked from.
	candidates = 50
	// whitespaceReason is the share of similar customers that makes "bought
	// by N of M similar customers" a reason.
	whitespaceReason = 0.2
	// MaxWithinDays bounds a reorder window, as a visit plan's period.
	MaxWithinDays = 366
	// peerMin is how many other buying customers make a peer group, as the
	// worker's PEER_MIN; peerReason the peer share that makes "bought by N
	// of M <group> customers" a reason; coldOrders the orders under which a
	// customer leans on its peers twice as much; peerCandidates how many of
	// its peers' most bought a customer's products also take.
	peerMin        = 3
	peerReason     = 0.2
	coldOrders     = 3
	peerCandidates = 20
	// nearSummary is how many products a summary names, earthKm the radius
	// haversine uses.
	nearSummary = 3
	earthKm     = 6371.0
)

// NearKmMin, NearKmMax, NearPeersMin and NearPeersMax bound a model's nearby
// peers (the training's near_km and near_peers), as the worker's
// NEAR_KM_BOUNDS and NEAR_PEERS_BOUNDS.
const (
	NearKmMin    = 0.1
	NearKmMax    = 500
	NearPeersMin = peerMin
	NearPeersMax = 200
)

// AllCustomers is the For of a reorder answer for every customer.
const AllCustomers = "all_customers"

// The re-ranking weights of the product scenarios, as the worker's
// PRODUCT_WEIGHTS and AUDIENCE_WEIGHTS. "peers" applies only to a model with
// customer categories or sizes, and to a customer with a peer group;
// "brand" to a model with brands and a customer who spends on them; "new"
// to a model with launch dates.
var (
	productWeights  = map[string]float64{"cf": 0.6, "whitespace": 0.3, "margin": 0.1, "peers": 0.2, "brand": 0.2, "new": 0.2}
	audienceWeights = map[string]float64{"cf": 0.6, "whitespace": 0.4, "peers": 0.3}
)

// The reason codes of a Pick.
const (
	ReasonReorderDue       = "reorder_due"
	ReasonChurnRisk        = "churn_risk"
	ReasonHighValue        = "high_value"
	ReasonWhitespace       = "whitespace"
	ReasonNotVisited       = "not_visited"
	ReasonSimilarCustomers = "similar_customers"
	ReasonPeerGroup        = "peer_group"
	ReasonBoughtTogether   = "bought_together"
	ReasonBoosted          = "boosted"
	ReasonPinned           = "pinned"
)

// ErrBadQuery marks a scenario call that is wrong in itself or for the
// model's scenario.
var ErrBadQuery = errors.New("invalid recommendation request")

func badQuery(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrBadQuery}, args...)...)
}

// Reason says in words, with numbers, why a result is listed.
type Reason struct {
	Code string `json:"code"`
	Text string `json:"text"`
}

// Pick is one result: a customer or a product, its score (higher is
// stronger; scores compare within one list only), its rank from 1, why, and
// what the model knows of it. DueDate is set by the reorder scenario, and
// Overdue when that day has passed with no order since. Products are the
// products a reorder customer is due to buy again: those due within the
// window, or the one product asked about.
type Pick struct {
	ID       string         `json:"id"`
	Score    float64        `json:"score"`
	Rank     int            `json:"rank"`
	Reasons  []Reason       `json:"reasons"`
	Fields   map[string]any `json:"fields"`
	DueDate  string         `json:"due_date,omitempty"`
	Overdue  bool           `json:"overdue,omitempty"`
	Products []DueProduct   `json:"products,omitempty"`
}

// DueProduct is a product a customer is due to buy again: when, from the
// customer's own gap between buying it, and whether that day has passed.
type DueProduct struct {
	ID      string         `json:"id"`
	DueDate string         `json:"due_date"`
	Overdue bool           `json:"overdue,omitempty"`
	Fields  map[string]any `json:"fields,omitempty"`
}

// PlanDay is one working day of a visit plan.
type PlanDay struct {
	Date  string `json:"date"`
	Items []Pick `json:"items"`
}

// Result answers one subject of a call: For is the customer, product or
// salesman asked about. Summary, for a customer's products, says in a line
// what the customer's nearby peers buy, such as "Minimarket customers
// within 5 km buy: A, B, C"; empty otherwise.
type Result struct {
	For     string    `json:"for"`
	Items   []Pick    `json:"items"`
	Plan    []PlanDay `json:"plan,omitempty"`
	Summary string    `json:"summary,omitempty"`
}

// Query is one scenario call. Exactly one subject is set: Customer,
// Customers, Product or Salesman, as the scenario allows. Territory, when
// set, is what a rule's from_request "territory" binds to; otherwise the
// territory most of the salesman's customers are in. From and To bound a
// visit plan (both days included); zero means the model's period from Today.
// K is how many results, 0 for the default. Rules are the request's, added
// to the model's own. AllCustomers asks a reorder model about every
// customer. WithinDays, for reorder, keeps the customers due to order before
// Today plus that many days, overdue ones included, ranked by how likely
// they are to order; 0 is no window. Segment and Size, for a list of
// customers (visits, reorder, audience), keep only the customers of that
// category and size, either matched in any case; a numeric Size is bucketed
// as the model's training cut its customers' sizes (SizeBucket). Brand and
// Subcategory, for a list of products (products, similar), keep only the
// products of that brand and subcategory, matched in any case. Stock is
// stock on hand by product id, in place of the model's (as of its
// training): a product with none is not offered.
type Query struct {
	Customer, Product, Salesman, Territory string
	Customers                              []string
	AllCustomers                           bool
	From, To                               time.Time
	Today                                  time.Time
	K                                      int
	Rules                                  []Rule
	ExcludeSeen                            bool
	WithinDays                             int
	Segment, Size                          string
	Brand, Subcategory                     string
	Stock                                  map[string]float64
}

// Answer answers a call of the model's scenario: one Result per subject.
//
//   - visits: for a salesman, their customers to visit, ranked and planned
//     across the working days of the period.
//   - products: for a customer (or a batch of customers), products to offer;
//     for a product, products like it.
//   - reorder: for a customer, a batch of them, a salesman's customers or
//     every customer, when each will order again (DueDate), most overdue
//     first; for a product, its repeat buyers by when they will buy it
//     again. With WithinDays, only those due before the window ends,
//     overdue ones included, most likely to order first.
//   - audience: for a product, the customers likely to want it.
//   - similar: for a product, products like it.
func (m *Model) Answer(q Query) ([]Result, error) {
	scenario := m.Scenario()
	if err := ValidateRules(scenario, q.Rules); err != nil {
		return nil, err
	}
	if err := CheckTightens(m.DefaultRules(), q.Rules); err != nil {
		return nil, err
	}
	if len(q.Customers) > MaxBatch {
		return nil, badQuery("at most %d customers per call, not %d", MaxBatch, len(q.Customers))
	}
	if q.WithinDays < 0 || q.WithinDays > MaxWithinDays {
		return nil, badQuery(`"within_days" is between 1 and %d days`, MaxWithinDays)
	}
	if q.WithinDays > 0 && scenario != ScenarioReorder {
		return nil, badQuery(`"within_days" applies to a reorder model ("When will they buy again"), not %s`, scenario)
	}
	if err := m.checkGroup(scenario, &q); err != nil {
		return nil, err
	}
	if err := m.checkKind(scenario, &q); err != nil {
		return nil, err
	}
	if err := checkStock(q.Stock); err != nil {
		return nil, err
	}
	if q.Today.IsZero() {
		q.Today = time.Now()
	}
	c := &call{m: m, q: q, today: dayOf(q.Today), scenario: scenario}
	subjects := 0
	for _, set := range []bool{q.Customer != "", len(q.Customers) > 0, q.Product != "", q.Salesman != "", q.AllCustomers} {
		if set {
			subjects++
		}
	}
	if subjects != 1 {
		return nil, badQuery(`give one of "customer", "customers", "product", "salesman" or "all_customers" in "for"`)
	}
	return c.answer()
}

// answer sends a call of one subject to its scenario.
func (c *call) answer() ([]Result, error) {
	one := func(r Result, err error) ([]Result, error) { return []Result{r}, err }
	scenario, q := c.scenario, c.q
	switch {
	case scenario == ScenarioVisits && q.Salesman != "":
		return one(c.visits())
	case scenario == ScenarioReorder && q.Salesman != "":
		return one(c.reorderSalesman())
	case scenario == ScenarioReorder && q.AllCustomers:
		return one(c.reorderAll())
	case scenario == ScenarioReorder && q.Product != "":
		return one(c.reorderProduct())
	case (scenario == ScenarioProducts || scenario == ScenarioReorder) && (q.Customer != "" || len(q.Customers) > 0):
		return c.perCustomer()
	case scenario == ScenarioAudience && q.Product != "":
		return one(c.audience())
	case (scenario == ScenarioSimilar || scenario == ScenarioProducts) && q.Product != "":
		return one(c.similar())
	}
	return nil, badQuery("scenario %q answers %s", scenario, allowedFor(scenario))
}

// perCustomer answers a products or reorder call of a customer or a batch:
// one result each, in order.
func (c *call) perCustomer() ([]Result, error) {
	ids := c.q.Customers
	if c.q.Customer != "" {
		ids = []string{c.q.Customer}
	}
	out := make([]Result, 0, len(ids))
	for _, id := range ids {
		var r Result
		var err error
		if c.scenario == ScenarioProducts {
			r, err = c.products(id)
		} else {
			r, err = c.reorderCustomer(id)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// checkGroup reads a request's category and size: they keep the customers of
// a customer list, so they are refused for products, and for a model that
// does not know them.
func (m *Model) checkGroup(scenario string, q *Query) error {
	q.Segment, q.Size = strings.TrimSpace(q.Segment), m.SizeBucket(q.Size)
	if q.Segment == "" && q.Size == "" {
		return nil
	}
	if itemEntity(scenario) != entityCustomers {
		return badQuery(`"segment" and "size" keep the customers of customer lists (visits, reorder, audience), not %s results`, scenario)
	}
	for _, f := range []struct{ attr, value, what string }{{"segment", q.Segment, "customer category"}, {"size", q.Size, "customer size"}} {
		if f.value == "" {
			continue
		}
		if m.sc == nil || m.sc.cAttrs[f.attr] == nil {
			return badQuery("this model was trained without a %s (customers.%s): train it again with one to keep only some customers", f.what, f.attr)
		}
	}
	return nil
}

// inGroup reports whether customer u is of the request's category and size.
func (c *call) inGroup(u int) bool {
	q := c.q
	return (q.Segment == "" || strings.EqualFold(c.m.cattr(u, "segment"), q.Segment)) &&
		(q.Size == "" || strings.EqualFold(c.m.cattr(u, "size"), q.Size))
}

// allowedFor says which subjects a scenario takes, for an error.
func allowedFor(scenario string) string {
	switch scenario {
	case ScenarioVisits:
		return `"for": {"salesman": ...}`
	case ScenarioProducts:
		return `"for": {"customer": ...}, {"customers": [...]} or {"product": ...}`
	case ScenarioReorder:
		return `"for": {"customer": ...}, {"customers": [...]}, {"salesman": ...}, {"product": ...} or {"all_customers": true}`
	}
	return `"for": {"product": ...}`
}

// call is one Answer in progress.
type call struct {
	m        *Model
	q        Query
	today    int
	scenario string
	// norms caches each numeric field's minimum and maximum for boosts.
	norms map[string][2]float64
	// groups caches the peer groups' item counts (groupCounts).
	groups map[peerKey]*groupCounts
	// located caches the customers nearby peers are looked for among
	// (locatedCustomers).
	located []locatedCustomer
}

// cand is a scored candidate: an index of a customer or an item. A reorder
// candidate has its due day, as a number and as text, and the products it
// is due for.
type cand struct {
	idx      int
	score    float64
	reasons  []Reason
	due      string
	dueDay   int
	products []DueProduct
	pinned   bool
}

// rules is the model's rules then the request's, without those whose
// from_request the request does not give (such a rule is skipped), and how
// many of them are the model's.
func (c *call) rules() ([]Rule, int) {
	keep := func(list []Rule) []Rule {
		out := make([]Rule, 0, len(list))
		for _, r := range list {
			if r.FromRequest == "" || c.bound(r.FromRequest) != "" {
				out = append(out, r)
			}
		}
		return out
	}
	defaults := keep(c.m.DefaultRules())
	return append(defaults, keep(c.q.Rules)...), len(defaults)
}

// bound is the request's value for a from_request.
func (c *call) bound(from string) string {
	switch from {
	case FromSalesman:
		return c.q.Salesman
	case FromTerritory:
		if c.q.Territory != "" {
			return c.q.Territory
		}
		return c.salesmanTerritory()
	}
	return ""
}

// salesmanTerritory is the territory most of the request's salesman's
// customers are in; ties go to the first in order.
func (c *call) salesmanTerritory() string {
	if c.m.sc == nil || c.q.Salesman == "" {
		return ""
	}
	counts := map[string]int{}
	best := ""
	for u := range c.m.users {
		if c.m.cattr(u, "salesman") != c.q.Salesman {
			continue
		}
		t := c.m.cattr(u, "territory")
		if t == "" {
			continue
		}
		counts[t]++
		if counts[t] > counts[best] {
			best = t
		}
	}
	return best
}

// field is an entity's value of a field name: its text, its number, and
// whether it has one.
func (c *call) field(entity, name string, idx int) (string, float64, bool) {
	m := c.m
	ids := m.users
	if entity == entityProducts {
		ids = m.items
	}
	if name == "id" {
		f, err := strconv.ParseFloat(ids[idx], 64)
		return ids[idx], numOr(f, err), true
	}
	if m.sc == nil {
		return "", math.NaN(), false
	}
	attrs, feats, index, width := m.sc.cAttrs, m.sc.cFeat, m.sc.cFeatIndex, len(m.sc.cFeatNames)
	if entity == entityProducts {
		attrs, feats, index, width = m.sc.iAttrs, m.sc.iFeat, m.sc.iFeatIndex, len(m.sc.iFeatNames)
	}
	if v, ok := attrs[name]; ok {
		if v[idx] == "" {
			return "", math.NaN(), false
		}
		f, err := strconv.ParseFloat(v[idx], 64)
		return v[idx], numOr(f, err), true
	}
	if j, ok := index[name]; ok {
		f := float64(feats[idx*width+j])
		if math.IsNaN(f) {
			return "", f, false
		}
		return strconv.FormatFloat(f, 'f', -1, 64), f, true
	}
	return "", math.NaN(), false
}

func numOr(f float64, err error) float64 {
	if err != nil {
		return math.NaN()
	}
	return f
}

// matches reports whether the entity at idx meets a predicate rule.
func (c *call) matches(r Rule, idx int) bool {
	entity, name, _ := splitField(r.Field)
	text, num, ok := c.field(entity, name, idx)
	value := r.Value
	if r.FromRequest != "" {
		value = c.bound(r.FromRequest)
	}
	same := func(v any) bool {
		want, _ := ID(v)
		if ok && want == text {
			return true
		}
		f, isNum := v.(float64)
		return ok && isNum && f == num
	}
	switch r.Op {
	case OpEq:
		return same(value)
	case OpNeq:
		return !same(value)
	case OpIn, OpNin:
		list, _ := value.([]any)
		found := slices.ContainsFunc(list, same)
		return found == (r.Op == OpIn)
	case OpGt, OpLt:
		f, _ := value.(float64)
		if !ok || math.IsNaN(num) {
			return false
		}
		if r.Op == OpGt {
			return num > f
		}
		return num < f
	}
	return false
}

// subjectAllowed reports whether the request's customer or product meets
// every filter that names its entity rather than the results'.
func (c *call) subjectAllowed(entity string, idx int) bool {
	rules, _ := c.rules()
	for _, r := range rules {
		if r.Kind != RuleFilter {
			continue
		}
		e, _, _ := splitField(r.Field)
		if e == entity && e != itemEntity(c.scenario) && !c.matches(r, idx) {
			return false
		}
	}
	return true
}

// norm is a numeric field's value scaled to [0, 1] over every customer or
// item; 0 when it has none.
func (c *call) norm(entity, name string, idx int) float64 {
	key := entity + "." + name
	if c.norms == nil {
		c.norms = map[string][2]float64{}
	}
	bounds, ok := c.norms[key]
	if !ok {
		n := len(c.m.users)
		if entity == entityProducts {
			n = len(c.m.items)
		}
		lo, hi := math.Inf(1), math.Inf(-1)
		for i := range n {
			if _, f, ok := c.field(entity, name, i); ok && !math.IsNaN(f) {
				lo, hi = min(lo, f), max(hi, f)
			}
		}
		bounds = [2]float64{lo, hi}
		c.norms[key] = bounds
	}
	_, f, ok := c.field(entity, name, idx)
	if !ok || math.IsNaN(f) || !(bounds[1] > bounds[0]) {
		return 0
	}
	return (f - bounds[0]) / (bounds[1] - bounds[0])
}

// finish applies the rules to scored candidates of the scenario's results
// and keeps the best k: blocks, filters and excludes drop, boosts add, pins
// go first, and diversity caps each value of a field. interacted reports
// whether a result was interacted with within the last days days (0: ever);
// nil when nothing can be.
func (c *call) finish(cands []cand, k int, interacted func(idx, days int) bool) ([]cand, error) {
	f := c.finisher(interacted)
	out := make([]cand, 0, len(cands))
	boosted := false
	for _, cd := range cands {
		if f.blocked[cd.idx] || !f.inGroup(cd.idx) || !f.keep(cd.idx, len(f.rules)) {
			continue
		}
		if f.boost(&cd) {
			boosted = true
		}
		out = append(out, cd)
	}
	// The candidates come ranked; only a boost re-ranks them. (Similar
	// products' popular fill scores on another scale, so it must not be
	// sorted in among the neighbours unasked.)
	if boosted {
		slices.SortStableFunc(out, byScore)
	}
	ordered, err := f.pin(out)
	if err != nil {
		return nil, err
	}
	return f.diversify(ordered, k), nil
}

// finisher is the rules of one call, as finish applies them.
type finisher struct {
	c          *call
	entity     string
	ids        []string
	index      map[string]int
	rules      []Rule
	defaults   int
	blocked    map[int]bool
	interacted func(idx, days int) bool
}

func (c *call) finisher(interacted func(idx, days int) bool) *finisher {
	f := &finisher{c: c, entity: itemEntity(c.scenario), ids: c.m.users, index: c.m.userIndex,
		blocked: map[int]bool{}, interacted: interacted}
	if f.entity == entityProducts {
		f.ids, f.index = c.m.items, c.m.itemIndex
	}
	f.rules, f.defaults = c.rules()
	for _, r := range f.rules {
		if r.Kind != RuleBlock {
			continue
		}
		for _, id := range r.IDs {
			if i, ok := f.index[id]; ok {
				f.blocked[i] = true
			}
		}
	}
	return f
}

// inGroup reports whether a customer result is of the request's category and
// size, and a product result of its brand and subcategory.
func (f *finisher) inGroup(idx int) bool {
	if f.entity == entityProducts {
		return f.c.inKind(idx)
	}
	return f.c.inGroup(idx)
}

// keep reports whether the filters and excludes among the first upTo rules
// let idx through.
func (f *finisher) keep(idx int, upTo int) bool {
	for _, r := range f.rules[:upTo] {
		switch r.Kind {
		case RuleFilter:
			if e, _, _ := splitField(r.Field); e == f.entity && !f.c.matches(r, idx) {
				return false
			}
		case RuleExclude:
			if f.interacted != nil && f.interacted(idx, r.Days) {
				return false
			}
		}
	}
	return true
}

// boost adds every boost rule's share to cd, with a reason each; whether
// any applied.
func (f *finisher) boost(cd *cand) bool {
	boosted := false
	for _, r := range f.rules {
		if r.Kind != RuleBoost {
			continue
		}
		add := f.boostOf(r, cd.idx)
		if add != 0 {
			cd.score += add
			cd.reasons = append(cd.reasons, Reason{Code: ReasonBoosted, Text: boostText(r, add)})
			boosted = true
		}
	}
	return boosted
}

func (f *finisher) boostOf(r Rule, idx int) float64 {
	switch {
	case len(r.IDs) > 0:
		if slices.Contains(r.IDs, f.ids[idx]) {
			return r.Weight
		}
		return 0
	case r.Op != "":
		if f.c.matches(r, idx) {
			return r.Weight
		}
		return 0
	}
	e, name, _ := splitField(r.Field)
	return r.Weight * f.c.norm(e, name, idx)
}

// pin puts the pinned ids first, in the order the rules list them, then the
// rest of out. A request's pin of what the model's own rules leave out is
// refused.
func (f *finisher) pin(out []cand) ([]cand, error) {
	var pinned []cand
	taken := map[int]bool{}
	for n, r := range f.rules {
		if r.Kind != RulePin {
			continue
		}
		for _, id := range r.IDs {
			i, ok := f.index[id]
			if !ok || f.blocked[i] || !f.inGroup(i) || taken[i] {
				continue
			}
			if n >= f.defaults && !f.keep(i, f.defaults) {
				return nil, fmt.Errorf("%w: pinning %q would list what the model's rules leave out", ErrLoosens, id)
			}
			pinned = append(pinned, pinnedCand(out, i))
			taken[i] = true
		}
	}
	ordered := pinned
	for _, cd := range out {
		if !taken[cd.idx] {
			ordered = append(ordered, cd)
		}
	}
	return ordered, nil
}

// pinnedCand is i as a pinned result: its scored candidate when out has
// one.
func pinnedCand(out []cand, i int) cand {
	pc := cand{idx: i}
	if j := slices.IndexFunc(out, func(x cand) bool { return x.idx == i }); j >= 0 {
		pc = out[j]
	}
	pc.pinned = true
	pc.reasons = append([]Reason{{Code: ReasonPinned, Text: "Always listed: pinned by a rule"}}, pc.reasons...)
	return pc
}

// diversify keeps the first k of ordered that the diversity caps allow;
// pinned results always go in, and count.
func (f *finisher) diversify(ordered []cand, k int) []cand {
	var caps []Rule
	for _, r := range f.rules {
		if r.Kind == RuleDiversity {
			caps = append(caps, r)
		}
	}
	final := make([]cand, 0, min(k, len(ordered)))
	counts := make([]map[string]int, len(caps))
	for i := range counts {
		counts[i] = map[string]int{}
	}
	value := func(r Rule, idx int) string {
		e, name, _ := splitField(r.Field)
		v, _, _ := f.c.field(e, name, idx)
		return v
	}
	for _, cd := range ordered {
		if len(final) == k {
			break
		}
		over := false
		for i, r := range caps {
			if counts[i][value(r, cd.idx)] >= r.MaxPer && !cd.pinned {
				over = true
			}
		}
		if over {
			continue
		}
		for i, r := range caps {
			counts[i][value(r, cd.idx)]++
		}
		final = append(final, cd)
	}
	return final
}

func boostText(r Rule, add float64) string {
	switch {
	case len(r.IDs) > 0:
		return fmt.Sprintf("On a promoted list (+%.2f)", add)
	case r.Op != "":
		return fmt.Sprintf("Boosted by a rule on %s (+%.2f)", r.Field, add)
	}
	return fmt.Sprintf("Boosted for its %s (+%.2f)", r.Field, add)
}

// picks renders candidates as Picks of customers or products.
func (c *call) picks(cands []cand, customers bool) []Pick {
	out := make([]Pick, len(cands))
	for i, cd := range cands {
		p := Pick{Score: float64(float32(cd.score)), Rank: i + 1, Reasons: cd.reasons, DueDate: cd.due,
			Overdue: cd.due != "" && cd.dueDay < c.today, Products: cd.products}
		if p.Reasons == nil {
			p.Reasons = []Reason{}
		}
		if customers {
			p.ID, p.Fields = c.m.users[cd.idx], c.customerFields(cd.idx)
		} else {
			p.ID, p.Fields = c.m.items[cd.idx], c.itemFields(cd.idx)
		}
		out[i] = p
	}
	return out
}

func (c *call) customerFields(u int) map[string]any {
	out := map[string]any{}
	if c.m.sc == nil {
		return out
	}
	for _, key := range []string{"name", "territory", "segment", "size", "salesman"} {
		if v := c.m.cattr(u, key); v != "" {
			out[key] = v
		}
	}
	lat, lon := c.m.cf(u, featLat), c.m.cf(u, featLon)
	if !math.IsNaN(lat) && !math.IsNaN(lon) {
		out["lat"], out["lon"] = lat, lon
	}
	return out
}

// itemFields are a product's fields, as the worker's _item_fields: its name,
// category, subcategory, brand, unit and pack size, as the model has them.
func (c *call) itemFields(i int) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"name", "category", "subcategory", "brand", "unit"} {
		if v := c.m.iattr(i, key); v != "" {
			out[key] = v
		}
	}
	if pack := c.m.itemDetail(i, featPackSize); finite(pack) && pack > 0 {
		out[featPackSize] = pack
	}
	return out
}

// k is the request's k, the default when it gives none, at most limit.
func (c *call) k(def, limit int) int {
	if c.q.K <= 0 {
		return min(def, limit)
	}
	return min(c.q.K, limit)
}

// ---------------------------------------------------------------------------
// Products and similar products
// ---------------------------------------------------------------------------

// products is a customer's products: candidates re-ranked by
// productWeights, as the worker's product_scores.
func (c *call) products(id string) (Result, error) {
	m := c.m
	res := Result{For: id, Items: []Pick{}}
	k := c.k(DefaultK, MaxK)
	u, known := m.userIndex[id]
	if known && !c.subjectAllowed(entityCustomers, u) {
		return res, nil
	}
	var cands []cand
	if !known {
		for n, j := range m.popular {
			cands = append(cands, cand{idx: int(j), score: float64(m.popularScores[n]), reasons: []Reason{popularReason}})
		}
		cands = c.offerable(cands, nil)
	} else {
		cands = c.productCandidates(u)
	}
	var interacted func(int, int) bool
	if known {
		interacted = func(item, days int) bool { return c.boughtWithin(u, item, days) }
	}
	final, err := c.finish(cands, k, interacted)
	if err != nil {
		return res, err
	}
	res.Items = c.picks(final, false)
	if known {
		res.Summary = c.nearbySummary(u, res.Items)
		c.suggest(u, res.Items)
	}
	return res, nil
}

var popularReason = Reason{Code: ReasonPopular, Text: "One of the most popular products"}

// candidate is one of a customer's product candidates before re-ranking:
// its collaborative-filtering score, and why it is one ("" for that score,
// ReasonPopular for popular fill, reasonPeers for its peers' most bought).
type candidate struct {
	item   int
	score  float64
	reason string
}

// reasonPeers marks a candidate taken from a customer's peers.
const reasonPeers = "peers"

// productCandidates scores a known customer's candidates, as the worker's
// product_scores: its collaborative-filtering candidates, then its peers'
// most bought, its brands' products and new products of its kind,
// re-ranked; what cannot be offered gives way to its substitute.
func (c *call) productCandidates(u int) []cand {
	m := c.m
	items, _ := m.ForUser(m.users[u], candidates, c.q.ExcludeSeen)
	best := 0.0
	list := make([]candidate, 0, len(items))
	for _, it := range items {
		if it.Reason == "" && it.Score > best {
			best = it.Score
		}
		list = append(list, candidate{item: m.itemIndex[it.Item], score: it.Score, reason: it.Reason})
	}
	peers := c.peersOf(u)
	if peers != nil {
		list = append(list, c.peerCandidates(u, peers, list)...)
	}
	brands := c.spendShares(u, "brand")
	var groups map[string]float64
	withNew := m.sc != nil && m.sc.newDays > 0 && m.sc.seenSpend != nil
	if withNew {
		groups = c.spendShares(u, m.groupKey())
	}
	weights, total := m.productWeights(c.peerWeight(u, productWeights["peers"], peers), len(brands) > 0, withNew)
	var mine []uint32
	if c.q.ExcludeSeen {
		mine = m.seenBy(u)
	}
	taken := map[int]bool{}
	for _, it := range list {
		taken[it.item] = true
	}
	for _, i := range mine {
		taken[int(i)] = true
	}
	if weights["brand"] > 0 {
		list = append(list, c.brandCandidatesOf(brands, taken)...)
	}
	if withNew {
		list = append(list, c.newCandidatesOf(brands, groups, taken)...)
	}
	out := make([]cand, 0, len(list))
	for _, it := range list {
		i := it.item
		cf := 0.0
		if it.reason == "" && best > 0 && it.score > 0 {
			cf = it.score / best
		}
		share, have, of := c.whitespaceShare(u, i)
		mp := 0.0
		if m.sc != nil {
			if v := m.itf(i, featMarginPct); !math.IsNaN(v) {
				mp = v
			}
		}
		ps, peerHave := peers.share(i)
		brand := m.iattr(i, "brand")
		bs := 0.0
		if brand != "" {
			bs = brands[brand]
		}
		fit, launch, isNew := 0.0, 0.0, false
		if withNew {
			if launch, isNew = c.launched(i); isNew {
				fit = c.fit(i, brands, groups)
			}
		}
		score := (weights["cf"]*cf + weights["whitespace"]*share + weights["margin"]*mp + weights["peers"]*ps +
			weights["brand"]*bs + weights["new"]*fit) / total
		peerText := ""
		if peerHave > 0 && (ps >= peerReason || it.reason == reasonPeers) {
			peerText = peers.reason(peerHave)
		}
		var details []Reason
		if bs >= brandReason {
			details = append(details, brandReasonOf(brand, bs))
		}
		if fit > 0 {
			details = append(details, newReason(launch))
		}
		reasons := productReasons(it.reason == ReasonPopular, share, have, of, peerText, details)
		out = append(out, cand{idx: i, score: float64(float32(score)), reasons: reasons})
	}
	// Stable: a tie keeps the candidates' order, so popular fill stays most
	// popular first.
	slices.SortStableFunc(out, byScore)
	return c.offerable(out, mine)
}

// productWeights is the re-ranking weights the model has the data for, with
// peers' weight when it is above 0, brand's for a customer who spends on
// brands and new's for a model with launch dates, and their sum in the
// worker's order.
func (m *Model) productWeights(peers float64, brands, withNew bool) (map[string]float64, float64) {
	weights := map[string]float64{"cf": productWeights["cf"]}
	if m.sc != nil && m.sc.cnb > 0 {
		weights["whitespace"] = productWeights["whitespace"]
	}
	if m.sc != nil && m.sc.hasMargin {
		weights["margin"] = productWeights["margin"]
	}
	if peers > 0 {
		weights["peers"] = peers
	}
	if brands {
		weights["brand"] = productWeights["brand"]
	}
	if withNew {
		weights["new"] = productWeights["new"]
	}
	total := 0.0
	for _, k := range []string{"cf", "whitespace", "margin", "peers", "brand", "new"} {
		total += weights[k]
	}
	return weights, total
}

// productReasons says why a product is offered: how many similar customers
// buy it when that is a fair share, how many of its peers do (peer, "" for
// none), its brand and newness (details), then popular fill or the general
// collaborative reason.
func productReasons(popular bool, share float64, have, of int, peer string, details []Reason) []Reason {
	var reasons []Reason
	if have > 0 && share >= whitespaceReason {
		reasons = append(reasons, Reason{Code: ReasonSimilarCustomers, Text: fmt.Sprintf("Bought by %d of %d similar customers", have, of)})
	}
	if peer != "" {
		reasons = append(reasons, Reason{Code: ReasonPeerGroup, Text: peer})
	}
	reasons = append(reasons, details...)
	switch {
	case popular:
		reasons = append(reasons, popularReason)
	case len(reasons) == 0:
		reasons = append(reasons, Reason{Code: ReasonSimilarCustomers, Text: "Customers who buy what this customer buys also buy it"})
	}
	return reasons
}

// byScore orders candidates best first.
func byScore(a, b cand) int {
	switch {
	case a.score > b.score:
		return -1
	case a.score < b.score:
		return 1
	}
	return 0
}

// byScoreThenIndex orders candidates best first, a tie by index.
func byScoreThenIndex(a, b cand) int {
	if c := byScore(a, b); c != 0 {
		return c
	}
	return a.idx - b.idx
}

// whitespaceShare is how many of a customer's similar customers have item:
// (share, count, of).
func (c *call) whitespaceShare(u, item int) (float64, int, int) {
	have, of := 0, 0
	for _, n := range c.m.customerNeighborsOf(u) {
		if n == empty {
			break
		}
		of++
		if has(c.m.seenBy(int(n)), uint32(item)) {
			have++
		}
	}
	if of == 0 {
		return 0, 0, 0
	}
	return float64(have) / float64(of), have, of
}

// ---------------------------------------------------------------------------
// Peers
// ---------------------------------------------------------------------------

// peerKey names a peer group: a category, a size or both; "" matches any.
type peerKey struct{ segment, size string }

// peerKeyOf is customer u's peer group; ok is false when the model has no
// categories or sizes, or u has neither.
func (c *call) peerKeyOf(u int) (peerKey, bool) {
	m := c.m
	if m.sc == nil || !m.sc.hasPeers {
		return peerKey{}, false
	}
	k := peerKey{m.cattr(u, "segment"), m.cattr(u, "size")}
	return k, k != peerKey{}
}

// matches reports whether customer v is of the group.
func (k peerKey) matches(m *Model, v int) bool {
	return (k.segment == "" || m.cattr(v, "segment") == k.segment) && (k.size == "" || m.cattr(v, "size") == k.size)
}

// label names the group in a reason, such as "Retail · large".
func (k peerKey) label() string {
	parts := make([]string, 0, 2)
	for _, p := range []string{k.segment, k.size} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " · ")
}

// peers is a customer's peer group, as the worker's Peers: how many of its
// buying customers, the customer left out, buy each item it was counted
// for, how many they are, its name, and for nearby peers the radius they
// are within in kilometres (0 for the whole group).
type peers struct {
	have   func(item int) int
	of     int
	label  string
	within float64
}

// share is the share of the peers who buy item, and how many do; 0 for no
// peer group.
func (p *peers) share(item int) (float64, int) {
	if p == nil {
		return 0, 0
	}
	have := p.have(item)
	return float64(have) / float64(p.of), have
}

func (p *peers) reason(have int) string {
	return fmt.Sprintf("Bought by %d of %d %s customers%s", have, p.of, p.label, p.where())
}

// where is " within 5 km" for nearby peers, "" for the whole group.
func (p *peers) where() string {
	if p.within == 0 {
		return ""
	}
	return " within " + strconv.FormatFloat(p.within, 'f', -1, 64) + " km"
}

// groupCounts is how many of a group's buying customers buy each item, and
// how many they are; counted once per call.
type groupCounts struct {
	buyers int
	counts map[uint32]int
}

func (c *call) groupCounts(k peerKey) *groupCounts {
	if g, ok := c.groups[k]; ok {
		return g
	}
	m := c.m
	g := &groupCounts{counts: map[uint32]int{}}
	for v := range m.users {
		seen := m.seenBy(v)
		if len(seen) == 0 || !k.matches(m, v) {
			continue
		}
		g.buyers++
		for _, i := range seen {
			g.counts[i]++
		}
	}
	if c.groups == nil {
		c.groups = map[peerKey]*groupCounts{}
	}
	c.groups[k] = g
	return g
}

// peersOf is customer u's peers, as the worker's peer_group: the other
// customers of its category and size who have bought anything, when at
// least peerMin; nil otherwise. When enough of them are near u (nearPeers),
// only those.
func (c *call) peersOf(u int) *peers {
	k, ok := c.peerKeyOf(u)
	if !ok {
		return nil
	}
	if near := c.nearPeers(u, k); near != nil {
		counts := map[uint32]int{}
		for _, v := range near {
			for _, i := range c.m.seenBy(v) {
				counts[i]++
			}
		}
		return &peers{of: len(near), label: k.label(), within: c.m.sc.nearKm, have: func(item int) int { return counts[uint32(item)] }}
	}
	g := c.groupCounts(k)
	seen := c.m.seenBy(u)
	of := g.buyers
	if len(seen) > 0 {
		of--
	}
	if of < peerMin {
		return nil
	}
	return &peers{of: of, label: k.label(), have: func(item int) int {
		n := g.counts[uint32(item)]
		if has(seen, uint32(item)) {
			n--
		}
		return n
	}}
}

// peerCounts is a group's buying customers and how many of them buy one item.
type peerCounts struct{ buyers, have int }

// peerItemCounts counts, for every peer group of the model's customers, its
// buying customers and how many of them buy item: what an audience reads of
// each customer's peers, in one pass.
func (c *call) peerItemCounts(item int) map[peerKey]peerCounts {
	m := c.m
	if m.sc == nil || !m.sc.hasPeers {
		return nil
	}
	out := map[peerKey]peerCounts{}
	for v := range m.users {
		seen := m.seenBy(v)
		if len(seen) == 0 {
			continue
		}
		bought := has(seen, uint32(item))
		// The groups v is of: its category and size, its category of any
		// size, and its size of any category.
		seg, size := m.cattr(v, "segment"), m.cattr(v, "size")
		var keys []peerKey
		if seg != "" && size != "" {
			keys = append(keys, peerKey{seg, size})
		}
		if seg != "" {
			keys = append(keys, peerKey{seg, ""})
		}
		if size != "" {
			keys = append(keys, peerKey{"", size})
		}
		for _, k := range keys {
			pc := out[k]
			pc.buyers++
			if bought {
				pc.have++
			}
			out[k] = pc
		}
	}
	return out
}

// audiencePeers is customer u's peers for one item: its nearby peers when
// there are enough, otherwise its group's counts from peerItemCounts.
func (c *call) audiencePeers(u, item int, groups map[peerKey]peerCounts) *peers {
	k, ok := c.peerKeyOf(u)
	if !ok {
		return nil
	}
	if near := c.nearPeers(u, k); near != nil {
		have := 0
		for _, v := range near {
			if has(c.m.seenBy(v), uint32(item)) {
				have++
			}
		}
		return &peers{of: len(near), label: k.label(), within: c.m.sc.nearKm, have: func(int) int { return have }}
	}
	pc := groups[k]
	seen := c.m.seenBy(u)
	if len(seen) > 0 {
		pc.buyers--
	}
	if pc.buyers < peerMin {
		return nil
	}
	if has(seen, uint32(item)) {
		pc.have--
	}
	return &peers{of: pc.buyers, label: k.label(), have: func(int) int { return pc.have }}
}

// locatedCustomer is a customer with coordinates who has bought anything.
type locatedCustomer struct {
	u        int
	lat, lon float64
}

// locatedCustomers are the customers nearby peers are looked for among, by
// latitude, a tie by index; found once per call.
func (c *call) locatedCustomers() []locatedCustomer {
	if c.located != nil {
		return c.located
	}
	m := c.m
	out := []locatedCustomer{}
	for v := range m.users {
		lat, lon := m.cf(v, featLat), m.cf(v, featLon)
		if len(m.seenBy(v)) > 0 && finite(lat) && finite(lon) {
			out = append(out, locatedCustomer{v, lat, lon})
		}
	}
	slices.SortFunc(out, func(a, b locatedCustomer) int {
		if a.lat != b.lat {
			return cmp.Compare(a.lat, b.lat)
		}
		return a.u - b.u
	})
	c.located = out
	return out
}

// nearPeers is customer u's nearby peers, as the worker's _near_peers: the
// model's nearPeers nearest other buying customers of group k within its
// nearKm (a tie by index), when u has coordinates and they are at least
// peerMin; nil otherwise.
func (c *call) nearPeers(u int, k peerKey) []int {
	m := c.m
	if m.sc == nil || m.sc.nearPeers == 0 {
		return nil
	}
	lat, lon := m.cf(u, featLat), m.cf(u, featLon)
	if !finite(lat) || !finite(lon) {
		return nil
	}
	located := c.locatedCustomers()
	// Two points are at least as far apart as their latitudes: only those
	// within nearKm of latitude can be near (the margin keeps every one the
	// distance itself would take).
	span := m.sc.nearKm/earthKm*180/math.Pi + 1e-9
	from, _ := slices.BinarySearchFunc(located, lat-span, func(l locatedCustomer, t float64) int { return cmp.Compare(l.lat, t) })
	type near struct {
		d float64
		v int
	}
	var found []near
	for _, l := range located[from:] {
		if l.lat > lat+span {
			break
		}
		if l.u == u || !k.matches(m, l.u) {
			continue
		}
		if d := haversine(lat, lon, l.lat, l.lon); d <= m.sc.nearKm {
			found = append(found, near{d, l.u})
		}
	}
	slices.SortFunc(found, func(a, b near) int {
		if a.d != b.d {
			return cmp.Compare(a.d, b.d)
		}
		return a.v - b.v
	})
	if len(found) > m.sc.nearPeers {
		found = found[:m.sc.nearPeers]
	}
	if len(found) < peerMin {
		return nil
	}
	out := make([]int, len(found))
	for i, f := range found {
		out[i] = f.v
	}
	return out
}

// nearbySummary says in a line what customer u's nearby peers buy, as the
// worker's nearby_summary: the first nearSummary of its picks they give a
// reason for, by name; "" when its peers are not nearby or give none.
func (c *call) nearbySummary(u int, picks []Pick) string {
	p := c.peersOf(u)
	if p == nil || p.within == 0 {
		return ""
	}
	var names []string
	for _, pk := range picks {
		if len(names) == nearSummary {
			break
		}
		if !slices.ContainsFunc(pk.Reasons, func(r Reason) bool { return r.Code == ReasonPeerGroup }) {
			continue
		}
		name, _ := pk.Fields["name"].(string)
		if name == "" {
			name = pk.ID
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("%s customers%s buy: %s", p.label, p.where(), strings.Join(names, ", "))
}

// peerWeight is the peers' weight for customer u: 0 without a peer group,
// twice base for a customer with fewer than coldOrders orders.
func (c *call) peerWeight(u int, base float64, p *peers) float64 {
	if p == nil {
		return 0
	}
	if orders := c.m.cf(u, featOrders); !(finite(orders) && orders >= coldOrders) {
		return 2 * base
	}
	return base
}

// peerCandidates are the peerCandidates items most of customer u's peers
// buy, not already listed (nor bought, when the request leaves those out),
// most bought first; a tie by index.
func (c *call) peerCandidates(u int, p *peers, listed []candidate) []candidate {
	taken := map[int]bool{}
	for _, it := range listed {
		taken[it.item] = true
	}
	seen := c.m.seenBy(u)
	type counted struct{ item, have int }
	var all []counted
	for i := range c.m.items {
		if taken[i] || (c.q.ExcludeSeen && has(seen, uint32(i))) {
			continue
		}
		if n := p.have(i); n > 0 {
			all = append(all, counted{i, n})
		}
	}
	slices.SortFunc(all, func(a, b counted) int {
		if a.have != b.have {
			return b.have - a.have
		}
		return a.item - b.item
	})
	out := make([]candidate, 0, min(len(all), peerCandidates))
	for _, it := range all[:min(len(all), peerCandidates)] {
		out = append(out, candidate{item: it.item, reason: reasonPeers})
	}
	return out
}

// boughtWithin reports whether customer u bought item within days days of
// today (0: ever).
func (c *call) boughtWithin(u, item, days int) bool {
	seen := c.m.seenBy(u)
	k, found := slices.BinarySearch(seen, uint32(item))
	if !found {
		return false
	}
	if days == 0 || c.m.sc == nil {
		return true
	}
	last := c.m.sc.seenLast[int(c.m.seenOff[u])+k]
	return last != empty && int(last) >= c.today-days
}

// similar is the products like the request's product.
func (c *call) similar() (Result, error) {
	m := c.m
	id := c.q.Product
	res := Result{For: id, Items: []Pick{}}
	i, known := m.itemIndex[id]
	if !known || len(m.items) == 0 {
		return res, nil
	}
	if !c.subjectAllowed(entityProducts, i) {
		return res, nil
	}
	items, _ := m.Similar(id, MaxK)
	together := Reason{Code: ReasonBoughtTogether, Text: "Often bought by the same customers as " + id}
	var cands []cand
	if m.iattr(i, "subcategory") != "" || m.iattr(i, "brand") != "" {
		cands = c.similarKinCands(i, items, together)
	} else {
		cands = make([]cand, 0, len(items))
		for _, it := range items {
			r := together
			if it.Reason == ReasonPopular {
				r = popularReason
			}
			cands = append(cands, cand{idx: m.itemIndex[it.Item], score: it.Score, reasons: []Reason{r}})
		}
	}
	final, err := c.finish(c.offerable(cands, nil), c.k(DefaultK, MaxK), nil)
	if err != nil {
		return res, err
	}
	res.Items = c.picks(final, false)
	return res, nil
}

// ---------------------------------------------------------------------------
// Audience
// ---------------------------------------------------------------------------

// audience is the customers most likely to want the request's product, as
// the worker's audience_scores.
func (c *call) audience() (Result, error) {
	m := c.m
	id := c.q.Product
	res := Result{For: id, Items: []Pick{}}
	item, known := m.itemIndex[id]
	if !known || !c.subjectAllowed(entityProducts, item) || c.unavailable(item) != "" {
		return res, nil
	}
	launch, isNew := 0.0, false
	if m.sc != nil && m.sc.seenSpend != nil {
		launch, isNew = c.launched(item)
	}
	cf := m.audienceCF(item)
	best := 0.0
	for _, v := range cf {
		best = max(best, v)
	}
	weights := map[string]float64{"cf": audienceWeights["cf"]}
	if m.sc != nil && m.sc.cnb > 0 {
		weights["whitespace"] = audienceWeights["whitespace"]
	}
	groups := c.peerItemCounts(item)
	var cands []cand
	for u := range m.users {
		if c.q.ExcludeSeen && has(m.seenBy(u), uint32(item)) {
			continue
		}
		share, have, of := c.whitespaceShare(u, item)
		v := 0.0
		if best > 0 && cf[u] > 0 {
			v = cf[u] / best
		}
		peers := c.audiencePeers(u, item, groups)
		pw := c.peerWeight(u, audienceWeights["peers"], peers)
		ps, peerHave := peers.share(item)
		wn, fit := 0.0, 0.0
		if isNew {
			wn, fit = audienceNew, c.fit(item, c.spendShares(u, "brand"), c.spendShares(u, m.groupKey()))
		}
		score := (weights["cf"]*v + weights["whitespace"]*share + pw*ps + wn*fit) / (weights["cf"] + weights["whitespace"] + pw + wn)
		if score <= 0 {
			continue
		}
		var reasons []Reason
		if have > 0 && share >= whitespaceReason {
			reasons = append(reasons, Reason{Code: ReasonSimilarCustomers, Text: fmt.Sprintf("%d of %d similar customers buy it", have, of)})
		}
		if peerHave > 0 && ps >= peerReason {
			reasons = append(reasons, Reason{Code: ReasonPeerGroup, Text: peers.reason(peerHave)})
		}
		if fit > 0 {
			reasons = append(reasons, newReason(launch))
		}
		if len(reasons) == 0 {
			reasons = []Reason{{Code: ReasonBoughtTogether, Text: "Buys products often bought with it"}}
		}
		cands = append(cands, cand{idx: u, score: float64(float32(score)), reasons: reasons})
	}
	slices.SortFunc(cands, byScoreThenIndex)
	final, err := c.finish(cands, c.k(DefaultK, MaxK), func(u, days int) bool { return c.boughtWithin(u, item, days) })
	if err != nil {
		return res, err
	}
	res.Items = c.picks(final, true)
	return res, nil
}

// audienceCF is every customer's collaborative-filtering affinity to item:
// the dot product of their factors for ALS, or the summed similarity of what
// they bought to it for item similarity.
func (m *Model) audienceCF(item int) []float64 {
	cf := make([]float64, len(m.users))
	switch {
	case m.factors > 0:
		vec := m.itemFactors[item*m.factors : (item+1)*m.factors]
		for u := range m.users {
			cf[u] = dot(m.userFactors[u*m.factors:(u+1)*m.factors], vec)
		}
	case m.neighbors > 0:
		sims := make([]float64, len(m.items))
		for j := range m.items {
			for s, n := range m.neighborsOf(j) {
				if n == uint32(item) {
					sims[j] = float64(m.nbrScores[j*m.neighbors+s])
				}
			}
		}
		for u := range m.users {
			for _, j := range m.seenBy(u) {
				cf[u] += sims[j]
			}
		}
	}
	return cf
}
