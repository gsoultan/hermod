package recommend

import (
	"fmt"
	"math"
	"slices"
)

// visitWeights are the model's visit weights that apply, summing to 1, as
// the worker's normalised_weights: neglect needs visits and opportunity
// needs similar customers, and a signal the data cannot give is dropped.
func (m *Model) visitWeights() map[string]float64 {
	w := map[string]float64{}
	for _, s := range signals {
		if m.sc.weights == nil {
			w[s] = DefaultWeights[s]
		} else {
			w[s] = m.sc.weights[s]
		}
	}
	if !m.sc.hasVisits {
		delete(w, SignalNeglect)
	}
	if m.sc.cnb == 0 {
		delete(w, SignalOpportunity)
	}
	total := 0.0
	for _, v := range w {
		total += v
	}
	for k, v := range w {
		if total <= 0 {
			w[k] = 1 / float64(len(w))
		} else {
			w[k] = v / total
		}
	}
	return w
}

func clip01(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0
	}
	return min(1, max(0, x))
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// gap is a customer's usual days between orders: their own median gap, or
// every customer's.
func (m *Model) gap(u int) float64 {
	if g := m.cf(u, featMedianGap); finite(g) && g > 0 {
		return g
	}
	return m.sc.globalGap
}

// overdue is the days from a customer's last order to day over their usual
// gap; NaN for a customer with no order.
func (m *Model) overdue(u, day int) float64 {
	last := m.cf(u, featLastOrder)
	if !finite(last) {
		return math.NaN()
	}
	return (float64(day) - last) / m.gap(u)
}

// visitSignals are a customer's five signals for a plan of period days from
// today, as the worker's visit_signals.
func (m *Model) visitSignals(u, today, period int, hasVisits bool) map[string]float64 {
	churn := clip01((m.overdue(u, today) - 1.5) / 1.5)
	out := map[string]float64{
		SignalReorderDue:  clip01((m.overdue(u, today+period)-0.5)/0.5) * (1 - churn),
		SignalChurnRisk:   churn,
		SignalValue:       clip01(m.cf(u, featValuePct)),
		SignalOpportunity: clip01(m.cf(u, featWhitespacePct)),
		SignalNeglect:     0,
	}
	if hasVisits {
		last := m.cf(u, featLastVisit)
		if !finite(last) {
			out[SignalNeglect] = 1
		} else {
			out[SignalNeglect] = clip01(((float64(today)-last)/m.visitTarget(u) - 0.5) / 1.5)
		}
	}
	return out
}

// visitTarget is a customer's usual days between visits.
func (m *Model) visitTarget(u int) float64 {
	if t := m.cf(u, featVisitGap); finite(t) && t > 0 {
		return t
	}
	return m.sc.globalVisitGap
}

// visitScore is the weighted sum of the signals, in the order the worker
// sums them.
func visitScore(sig, weights map[string]float64) float64 {
	score := 0.0
	for _, s := range signals {
		if w, ok := weights[s]; ok {
			score += w * sig[s]
		}
	}
	return score
}

// round is Python's round(): halves go to the even neighbour.
func round(x float64) int { return int(math.RoundToEven(x)) }

// visitReasons says why a customer is on a plan, strongest first, as the
// worker's visit_reasons.
func (m *Model) visitReasons(u, today int, sig, weights map[string]float64) []Reason {
	type weighed struct {
		w     float64
		order int
		r     Reason
	}
	var out []weighed
	since := float64(today) - m.cf(u, featLastOrder)
	own := m.cf(u, featMedianGap)
	gap := m.gap(u)
	if sig[SignalReorderDue] > 0 {
		who := "Usually orders"
		if !(finite(own) && own > 0) {
			who = "Customers usually order"
		}
		text := fmt.Sprintf("%s every %d days; last order %d days ago", who, round(gap), round(since))
		out = append(out, weighed{weights[SignalReorderDue] * sig[SignalReorderDue], 0, Reason{ReasonReorderDue, text}})
	}
	if sig[SignalChurnRisk] > 0 {
		out = append(out, weighed{weights[SignalChurnRisk] * sig[SignalChurnRisk], 1, Reason{ReasonChurnRisk,
			fmt.Sprintf("No order for %d days, %.1f times the usual %d-day gap", round(since), since/gap, round(gap))}})
	}
	if pct := m.cf(u, featValuePct); finite(pct) && pct >= 0.75 {
		out = append(out, weighed{weights[SignalValue] * sig[SignalValue], 2, Reason{ReasonHighValue,
			fmt.Sprintf("Top %d%% of customers by revenue", max(1, round((1-pct)*100)))}})
	}
	if ws := m.cf(u, featWhitespace); finite(ws) && ws >= 1 && sig[SignalOpportunity] >= 0.5 {
		out = append(out, weighed{weights[SignalOpportunity] * sig[SignalOpportunity], 3, Reason{ReasonWhitespace,
			fmt.Sprintf("Similar customers buy %d products this customer has never bought", int(ws))}})
	}
	if sig[SignalNeglect] > 0 {
		text := "No visit on record"
		if last := m.cf(u, featLastVisit); finite(last) {
			text = fmt.Sprintf("Not visited for %d days (target every %d)", round(float64(today)-last), round(m.visitTarget(u)))
		}
		out = append(out, weighed{weights[SignalNeglect] * sig[SignalNeglect], 4, Reason{ReasonNotVisited, text}})
	}
	slices.SortFunc(out, func(a, b weighed) int {
		switch {
		case a.w > b.w:
			return -1
		case a.w < b.w:
			return 1
		}
		return a.order - b.order
	})
	reasons := make([]Reason, len(out))
	for i, w := range out {
		reasons[i] = w.r
	}
	return reasons
}

// pool is the customers of the request's salesman who are not blocked, in
// the model's order.
func (c *call) pool() ([]int, error) {
	m := c.m
	if m.sc == nil {
		return nil, badQuery("this model was trained without a customers table, so it knows no salesmen")
	}
	if _, ok := m.sc.cAttrs["salesman"]; !ok {
		return nil, badQuery("this model was trained without a salesman column in its customers table")
	}
	var out []int
	for u := range m.users {
		if m.cattr(u, "salesman") == c.q.Salesman && m.cattr(u, "blocked") != "true" {
			out = append(out, u)
		}
	}
	return out, nil
}

// span is the plan's first day and its length in days: From to To, or the
// model's period from today.
func (c *call) span() (int, int, error) {
	start := c.today
	if !c.q.From.IsZero() {
		start = dayOf(c.q.From)
	}
	period := c.m.PeriodDays()
	if !c.q.To.IsZero() {
		period = dayOf(c.q.To) - start + 1
	}
	if period < 1 || period > 366 {
		return 0, 0, badQuery(`"period" must run forward, at most 366 days`)
	}
	return start, period, nil
}

// capacity is the request's salesman's visits a day and working days: the
// capacity rules (the lowest per_day; the days every rule allows), else the
// salesmen table's, else DefaultVisitsPerDay on Monday to Friday.
func (c *call) capacity() (int, []string) {
	perDay := 0
	var days []string
	if s, ok := c.m.sc.salesmen[c.q.Salesman]; ok {
		if s.VisitsPerDay != nil {
			perDay = max(1, int(*s.VisitsPerDay))
		}
		if len(s.WorkingDays) > 0 {
			days = s.WorkingDays
		}
	}
	ruled, ruledDays := c.ruledCapacity()
	if ruled > 0 {
		perDay = ruled
	}
	if ruledDays != nil {
		days = ruledDays
	}
	if perDay == 0 {
		perDay = DefaultVisitsPerDay
	}
	if days == nil {
		days = weekdays[:5]
	}
	return min(perDay, MaxPlan), days
}

// ruledCapacity is what the capacity rules say: the lowest per_day (0 for
// none) and the weekdays every rule allows (nil when none lists any).
func (c *call) ruledCapacity() (int, []string) {
	ruled := 0
	var days []string
	rules, _ := c.rules()
	for _, r := range rules {
		if r.Kind != RuleCapacity {
			continue
		}
		if r.PerDay > 0 && (ruled == 0 || r.PerDay < ruled) {
			ruled = r.PerDay
		}
		list, _ := ruleWeekdays(r.Value)
		switch {
		case len(list) == 0:
		case days == nil:
			days = list
		default:
			days = slices.DeleteFunc(days, func(d string) bool { return !slices.Contains(list, d) })
		}
	}
	return ruled, days
}

// workingDays are the day numbers of [start, start+period) on days.
func workingDays(days []string, start, period int) []int {
	var out []int
	for d := start; d < start+period; d++ {
		// 1970-01-01 was a Thursday.
		if slices.Contains(days, weekdays[(d+3)%7]) {
			out = append(out, d)
		}
	}
	return out
}

// visits is a salesman's plan: their customers ranked by visit score, the
// best that fit in the period's capacity, scheduled across its working days.
func (c *call) visits() (Result, error) {
	m := c.m
	res := Result{For: c.q.Salesman, Items: []Pick{}, Plan: []PlanDay{}}
	pool, err := c.pool()
	if err != nil {
		return res, err
	}
	start, period, err := c.span()
	if err != nil {
		return res, err
	}
	perDay, days := c.capacity()
	dates := workingDays(days, start, period)
	room := min(perDay*len(dates), MaxPlan)
	weights := m.visitWeights()
	cands := make([]cand, 0, len(pool))
	for _, u := range pool {
		sig := m.visitSignals(u, start, period, m.sc.hasVisits)
		cands = append(cands, cand{idx: u, score: float64(float32(visitScore(sig, weights))), reasons: m.visitReasons(u, start, sig, weights)})
	}
	slices.SortFunc(cands, byScoreThenIndex)
	visited := func(u, within int) bool {
		last := m.cf(u, featLastVisit)
		return finite(last) && (within == 0 || int(last) >= start-within)
	}
	final, err := c.finish(cands, c.k(room, room), visited)
	if err != nil {
		return res, err
	}
	res.Items = c.picks(final, true)
	res.Plan = schedule(res.Items, dates, perDay)
	return res, nil
}

// schedule spreads a ranked list over working days, perDay a day. Each day
// starts with the most urgent customer left and fills up with, in turn, the
// one nearest the last added among the next 2 x perDay most urgent, by
// great-circle distance between their lat and lon. A customer without both
// is taken in rank order. What does not fit is left off the plan; Items
// still lists it.
func schedule(items []Pick, dates []int, perDay int) []PlanDay {
	plan := make([]PlanDay, 0, len(dates))
	left := slices.Clone(items)
	for _, d := range dates {
		day := PlanDay{Date: dayText(d), Items: []Pick{}}
		for len(day.Items) < perDay && len(left) > 0 {
			next := 0
			if n := len(day.Items); n > 0 {
				next = nearest(day.Items[n-1], left[:min(len(left), 2*perDay)])
			}
			day.Items = append(day.Items, left[next])
			left = slices.Delete(left, next, next+1)
		}
		if len(day.Items) > 0 {
			plan = append(plan, day)
		}
	}
	return plan
}

// nearest is the index in among of the customer nearest from; 0 when from
// or none of them has coordinates.
func nearest(from Pick, among []Pick) int {
	lat, lon, ok := coords(from)
	if !ok {
		return 0
	}
	next, best := 0, math.Inf(1)
	for i, p := range among {
		if la, lo, ok := coords(p); ok {
			if dist := haversine(lat, lon, la, lo); dist < best {
				best, next = dist, i
			}
		}
	}
	return next
}

func coords(p Pick) (float64, float64, bool) {
	lat, ok1 := p.Fields["lat"].(float64)
	lon, ok2 := p.Fields["lon"].(float64)
	return lat, lon, ok1 && ok2
}

// haversine is the great-circle distance in kilometres.
func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371.0
	rad := math.Pi / 180
	dLat, dLon := (lat2-lat1)*rad, (lon2-lon1)*rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Asin(math.Sqrt(min(1, a)))
}

// ---------------------------------------------------------------------------
// Reorder
// ---------------------------------------------------------------------------

// reorderPick is a customer's due date and overdue ratio; ok is false for a
// customer with no order.
func (c *call) reorderPick(u int) (cand, bool) {
	m := c.m
	last := m.cf(u, featLastOrder)
	if m.sc == nil || !finite(last) {
		return cand{}, false
	}
	gap := m.gap(u)
	due := dayText(int(math.Ceil(last + gap)))
	ratio := (float64(c.today) - last) / gap
	return cand{idx: u, score: float64(float32(ratio)), reasons: m.reorderReasons(u, c.today), due: due}, true
}

// reorderReasons are a customer's reorder_due and churn_risk reasons, as the
// worker's _reorder_reasons.
func (m *Model) reorderReasons(u, today int) []Reason {
	sig := m.visitSignals(u, today, 7, false)
	var out []Reason
	for _, r := range m.visitReasons(u, today, sig, DefaultWeights) {
		if r.Code == ReasonReorderDue || r.Code == ReasonChurnRisk {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		out = []Reason{{ReasonReorderDue, fmt.Sprintf("Usually orders every %d days; last order %d days ago",
			round(m.gap(u)), round(float64(today)-m.cf(u, featLastOrder)))}}
	}
	return out
}

// reorderCustomer is one customer's due date, as a one-item result.
func (c *call) reorderCustomer(id string) (Result, error) {
	res := Result{For: id, Items: []Pick{}}
	u, ok := c.m.userIndex[id]
	if !ok {
		return res, nil
	}
	cd, ok := c.reorderPick(u)
	if !ok {
		return res, nil
	}
	final, err := c.finish([]cand{cd}, 1, c.ordered)
	if err != nil {
		return res, err
	}
	res.Items = c.picks(final, true)
	return res, nil
}

// reorderSalesman is a salesman's customers by when they will order again,
// most overdue first.
func (c *call) reorderSalesman() (Result, error) {
	res := Result{For: c.q.Salesman, Items: []Pick{}}
	pool, err := c.pool()
	if err != nil {
		return res, err
	}
	var cands []cand
	for _, u := range pool {
		if cd, ok := c.reorderPick(u); ok {
			cands = append(cands, cd)
		}
	}
	slices.SortFunc(cands, byScoreThenIndex)
	final, err := c.finish(cands, c.k(DefaultK, MaxPlan), c.ordered)
	if err != nil {
		return res, err
	}
	res.Items = c.picks(final, true)
	return res, nil
}

// ordered reports whether customer u ordered within days days (0: ever).
func (c *call) ordered(u, days int) bool {
	last := c.m.cf(u, featLastOrder)
	return finite(last) && (days == 0 || int(last) >= c.today-days)
}
