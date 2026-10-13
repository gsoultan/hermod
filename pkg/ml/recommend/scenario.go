package recommend

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
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
)

// AllCustomers is the For of a reorder answer for every customer.
const AllCustomers = "all_customers"

// The re-ranking weights of the product scenarios, as the worker's
// PRODUCT_WEIGHTS and AUDIENCE_WEIGHTS.
var (
	productWeights  = map[string]float64{"cf": 0.6, "whitespace": 0.3, "margin": 0.1}
	audienceWeights = map[string]float64{"cf": 0.6, "whitespace": 0.4}
)

// The reason codes of a Pick.
const (
	ReasonReorderDue       = "reorder_due"
	ReasonChurnRisk        = "churn_risk"
	ReasonHighValue        = "high_value"
	ReasonWhitespace       = "whitespace"
	ReasonNotVisited       = "not_visited"
	ReasonSimilarCustomers = "similar_customers"
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
// salesman asked about.
type Result struct {
	For   string    `json:"for"`
	Items []Pick    `json:"items"`
	Plan  []PlanDay `json:"plan,omitempty"`
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
// they are to order; 0 is no window.
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
		if f.blocked[cd.idx] || !f.keep(cd.idx, len(f.rules)) {
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
			if !ok || f.blocked[i] || taken[i] {
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
	for _, key := range []string{"name", "territory", "segment", "salesman"} {
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

func (c *call) itemFields(i int) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"name", "category"} {
		if v := c.m.iattr(i, key); v != "" {
			out[key] = v
		}
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
	return res, nil
}

var popularReason = Reason{Code: ReasonPopular, Text: "One of the most popular products"}

// productCandidates scores a known customer's candidates.
func (c *call) productCandidates(u int) []cand {
	m := c.m
	items, _ := m.ForUser(m.users[u], candidates, c.q.ExcludeSeen)
	best := 0.0
	for _, it := range items {
		if it.Reason == "" && it.Score > best {
			best = it.Score
		}
	}
	weights, total := m.productWeights()
	out := make([]cand, 0, len(items))
	for _, it := range items {
		i := m.itemIndex[it.Item]
		cf := 0.0
		if it.Reason == "" && best > 0 && it.Score > 0 {
			cf = it.Score / best
		}
		share, have, of := c.whitespaceShare(u, i)
		mp := 0.0
		if m.sc != nil {
			if v := m.itf(i, featMarginPct); !math.IsNaN(v) {
				mp = v
			}
		}
		score := (weights["cf"]*cf + weights["whitespace"]*share + weights["margin"]*mp) / total
		out = append(out, cand{idx: i, score: float64(float32(score)), reasons: productReasons(it.Reason == ReasonPopular, share, have, of)})
	}
	// Stable: a tie keeps the candidates' order, so popular fill stays most
	// popular first.
	slices.SortStableFunc(out, byScore)
	return out
}

// productWeights is the re-ranking weights the model has the data for, and
// their sum.
func (m *Model) productWeights() (map[string]float64, float64) {
	weights := map[string]float64{"cf": productWeights["cf"]}
	if m.sc != nil && m.sc.cnb > 0 {
		weights["whitespace"] = productWeights["whitespace"]
	}
	if m.sc != nil && m.sc.hasMargin {
		weights["margin"] = productWeights["margin"]
	}
	total := 0.0
	for _, w := range weights {
		total += w
	}
	return weights, total
}

// productReasons says why a product is offered: how many similar customers
// buy it when that is a fair share, then popular fill or the general
// collaborative reason.
func productReasons(popular bool, share float64, have, of int) []Reason {
	var reasons []Reason
	if have > 0 && share >= whitespaceReason {
		reasons = append(reasons, Reason{Code: ReasonSimilarCustomers, Text: fmt.Sprintf("Bought by %d of %d similar customers", have, of)})
	}
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
	cands := make([]cand, 0, len(items))
	for _, it := range items {
		r := Reason{Code: ReasonBoughtTogether, Text: "Often bought by the same customers as " + id}
		if it.Reason == ReasonPopular {
			r = popularReason
		}
		cands = append(cands, cand{idx: m.itemIndex[it.Item], score: it.Score, reasons: []Reason{r}})
	}
	final, err := c.finish(cands, c.k(DefaultK, MaxK), nil)
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
	if !known || !c.subjectAllowed(entityProducts, item) {
		return res, nil
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
	total := weights["cf"] + weights["whitespace"]
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
		score := (weights["cf"]*v + weights["whitespace"]*share) / total
		if score <= 0 {
			continue
		}
		reason := Reason{Code: ReasonBoughtTogether, Text: "Buys products often bought with it"}
		if have > 0 && share >= whitespaceReason {
			reason = Reason{Code: ReasonSimilarCustomers, Text: fmt.Sprintf("%d of %d similar customers buy it", have, of)}
		}
		cands = append(cands, cand{idx: u, score: float64(float32(score)), reasons: []Reason{reason}})
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
