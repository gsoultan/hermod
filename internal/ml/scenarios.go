// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package ml

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gsoultan/hermod/internal/ml/monitor"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/recommend"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// Recommendation scenarios (docs/ml.md, "Recommendation scenarios"): a model
// trained for one business question — visits, products, reorder, audience
// or similar — answered in-process from its artifact (pkg/ml/recommend).

// The data roles a scenario's training maps.
var scenarioRoles = []string{"transactions", "customers", "products", "visits", "salesmen", "returns", "quotes"}

// isScenario reports whether a recommendation training names a scenario or
// data roles rather than #268's one-table recommend columns.
func isScenario(spec worker.TrainSpec) bool {
	return spec.Task == worker.TaskRecommendation && (spec.Scenario != "" || spec.Data != nil)
}

// scenarioSpec checks a scenario's training as far as Hermod can without the
// data, which the worker checks, and fills Dataset with the transactions
// dataset: a training is listed, retrained and counted by its dataset. A
// Dataset already given must be that one; Target is ignored, as the worker
// does, since the UI sends the train body's empty target with it.
func scenarioSpec(spec worker.TrainSpec) (worker.TrainSpec, error) {
	bad := func(format string, args ...any) (worker.TrainSpec, error) {
		return spec, fmt.Errorf("%w: "+format, append([]any{ErrBadTraining}, args...)...)
	}
	scenario := spec.Scenario
	if scenario == "" {
		scenario = recommend.ScenarioProducts
	}
	if !slices.Contains(recommend.Scenarios, scenario) {
		return bad("scenario %q is not one of %s", spec.Scenario, strings.Join(recommend.Scenarios, ", "))
	}
	if spec.Recommend != nil {
		return bad(`give "recommend" (one table, #268) or "scenario" and "data", not both`)
	}
	for role := range spec.Data {
		if !slices.Contains(scenarioRoles, role) {
			return bad("data role %q is not one of %s", role, strings.Join(scenarioRoles, ", "))
		}
	}
	tx := spec.Data["transactions"]["dataset"]
	if tx == "" {
		return bad(`a scenario needs "data.transactions" with its dataset`)
	}
	if spec.Dataset != "" && spec.Dataset != tx {
		return bad(`"dataset" must be the transactions dataset %q, or left out`, tx)
	}
	if spec.PeriodDays < 0 || spec.PeriodDays > 366 {
		return bad("period_days must be between 1 and 366")
	}
	if spec.NearKm != 0 && !(spec.NearKm >= recommend.NearKmMin && spec.NearKm <= recommend.NearKmMax) {
		return bad("near_km must be between %v and %v kilometres", recommend.NearKmMin, recommend.NearKmMax)
	}
	if spec.NearPeers != 0 && (spec.NearPeers < recommend.NearPeersMin || spec.NearPeers > recommend.NearPeersMax) {
		return bad("near_peers must be between %d and %d customers", recommend.NearPeersMin, recommend.NearPeersMax)
	}
	if spec.NewDays != 0 && (spec.NewDays < recommend.NewDaysMin || spec.NewDays > recommend.NewDaysMax) {
		return bad("new_days must be between %d and %d days", recommend.NewDaysMin, recommend.NewDaysMax)
	}
	if err := recommend.ValidateRules(scenario, spec.Rules); err != nil {
		return bad("%v", err)
	}
	switch {
	case spec.Device == DeviceGPU:
		return bad("a recommendation model trains on the CPU of the main worker")
	case strings.HasPrefix(spec.Algorithm, CustomPrefix):
		return bad("a recommendation model trains with als, item_similarity or auto, not a script")
	case spec.Preset != "":
		return bad("a preset does not apply to a recommendation model")
	}
	spec.Dataset, spec.Target = tx, ""
	return spec, nil
}

// RecommendCheck is a recommendation scenario's data health check, on the
// main worker, which holds the datasets: whether each role's dataset and
// columns are there and what vendors' minimums they meet. A body that names
// neither a scenario nor #268's recommend columns has nothing to check.
func (s *Service) RecommendCheck(ctx context.Context, vhost string, spec worker.TrainSpec) (worker.RecommendHealth, error) {
	if spec.Task != worker.TaskRecommendation || (!isScenario(spec) && spec.Recommend == nil) {
		return worker.RecommendHealth{}, fmt.Errorf(`%w: the data health check is for a recommendation model: `+
			`send task "recommendation" with "scenario" and "data"`, ErrBadTraining)
	}
	if isScenario(spec) {
		var err error
		if spec, err = scenarioSpec(spec); err != nil {
			return worker.RecommendHealth{}, err
		}
	}
	spec.Rules = nil
	w, err := s.Worker()
	if err != nil {
		return worker.RecommendHealth{}, err
	}
	return w.RecommendCheck(ctx, vhost, spec)
}

// ScenarioFor names who or what a scenario call is for. Ids are as JSON
// gives them: a string, or a number for an id column that held numbers.
// Territory, given here or at the top of the request, is what a rule's
// from_request "territory" binds to. AllCustomers asks a reorder model about
// every customer. Segment and Size keep only the customers of that category
// and size in a list of customers (visits, reorder, audience); a size is a
// label such as "large", or a number the model buckets as its training did.
// PriceTier, Channel and Region keep them likewise to one price tier,
// channel and region. Brand and Subcategory keep only the products of that brand and
// subcategory in a list of products (products, similar).
type ScenarioFor struct {
	Customer     any   `json:"customer,omitempty"`
	Customers    []any `json:"customers,omitempty"`
	Product      any   `json:"product,omitempty"`
	Salesman     any   `json:"salesman,omitempty"`
	Territory    any   `json:"territory,omitempty"`
	AllCustomers bool  `json:"all_customers,omitempty"`
	Segment      any   `json:"segment,omitempty"`
	Size         any   `json:"size,omitempty"`
	PriceTier    any   `json:"price_tier,omitempty"`
	Channel      any   `json:"channel,omitempty"`
	Region       any   `json:"region,omitempty"`
	Brand        any   `json:"brand,omitempty"`
	Subcategory  any   `json:"subcategory,omitempty"`
}

// ScenarioPeriod is a visit plan's days, both included, as YYYY-MM-DD. To
// may be left out: the model's period from From.
type ScenarioPeriod struct {
	From string `json:"from"`
	To   string `json:"to,omitempty"`
}

// ScenarioRequest is one call of POST .../recommend. Respond and
// CallbackURL ask for an asynchronous answer, which the REST API gives by
// queueing the call as a job; RecommendScenario itself answers at once, and
// refuses them. WithinDays, for a reorder model, keeps the customers due to
// order within that many days from today, overdue ones included. Stock is
// today's stock on hand by product id, in place of what the model learned
// at training: a product with none is not offered, or its substitute is.
type ScenarioRequest struct {
	For         ScenarioFor        `json:"for"`
	Territory   any                `json:"territory,omitempty"`
	Period      *ScenarioPeriod    `json:"period,omitempty"`
	WithinDays  int                `json:"within_days,omitempty"`
	K           int                `json:"k,omitempty"`
	Rules       []recommend.Rule   `json:"rules,omitempty"`
	ExcludeSeen *bool              `json:"exclude_seen,omitempty"`
	Stock       map[string]float64 `json:"stock,omitempty"`
	Respond     string             `json:"respond,omitempty"`
	CallbackURL string             `json:"callback_url,omitempty"`
}

// ScenarioAnswer is a scenario call's answer: one result per subject, and
// the id feedback on it is sent with.
type ScenarioAnswer struct {
	RecommendationID string             `json:"recommendation_id"`
	Results          []recommend.Result `json:"results"`
}

// newRecommendationID is a random id for one call's answer.
func newRecommendationID() string {
	return "rec_" + strings.ToLower(rand.Text())
}

func badRecommend(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrBadRecommendRequest}, args...)...)
}

// query reads the request into a recommend.Query for a model of scenario.
func (r ScenarioRequest) query(scenario string, today time.Time) (recommend.Query, int, error) {
	if err := r.checkRespond(); err != nil {
		return recommend.Query{}, 0, err
	}
	q := recommend.Query{Today: today, K: r.K, Rules: r.Rules, ExcludeSeen: r.ExcludeSeen == nil || *r.ExcludeSeen, Stock: r.Stock}
	if err := r.subjects(&q); err != nil {
		return q, 0, err
	}
	limit := recommend.MaxK
	if scenario == recommend.ScenarioVisits || (scenario == recommend.ScenarioReorder && (q.Salesman != "" || q.Product != "" || q.AllCustomers)) {
		limit = recommend.MaxPlan
	}
	if r.K < 0 || r.K > limit {
		return q, 0, badRecommend(`"k" is how many results, 1 to %d`, limit)
	}
	if err := r.period(scenario, &q); err != nil {
		return q, 0, err
	}
	if err := r.window(scenario, &q); err != nil {
		return q, 0, err
	}
	rows := max(1, len(q.Customers))
	return q, rows, nil
}

// window reads a reorder call's "within_days" into q.
func (r ScenarioRequest) window(scenario string, q *recommend.Query) error {
	if r.WithinDays == 0 {
		return nil
	}
	if r.WithinDays < 0 || r.WithinDays > recommend.MaxWithinDays {
		return badRecommend(`"within_days" is between 1 and %d days`, recommend.MaxWithinDays)
	}
	if scenario != recommend.ScenarioReorder {
		return badRecommend(`"within_days" applies to a reorder model ("When will they buy again")`)
	}
	q.WithinDays = r.WithinDays
	return nil
}

// checkRespond refuses an asynchronous answer: one is asked for on POST
// .../recommend, which queues the call as a job, and this call answers at
// once.
func (r ScenarioRequest) checkRespond() error {
	switch r.Respond {
	case "", "sync":
	case "async":
		return badRecommend(`"respond": "async" is for POST .../recommend, which runs the call as a job; this call answers synchronously`)
	default:
		return badRecommend(`"respond" is "sync" or "async"`)
	}
	if r.CallbackURL != "" {
		return badRecommend(`"callback_url" goes with "respond": "async" on POST .../recommend; this call answers synchronously`)
	}
	return nil
}

// check reads what of the request does not depend on the model's scenario
// (the ids, k up to the largest any scenario takes, the window's bounds and
// the period's dates),
// so a job is refused for it before it is queued. query reads the rest.
func (r ScenarioRequest) check() error {
	var q recommend.Query
	if err := r.subjects(&q); err != nil {
		return err
	}
	if limit := max(recommend.MaxK, recommend.MaxPlan); r.K < 0 || r.K > limit {
		return badRecommend(`"k" is how many results, 1 to %d`, limit)
	}
	if len(r.Stock) > recommend.MaxStock {
		return badRecommend(`at most %d products' "stock" per call, not %d`, recommend.MaxStock, len(r.Stock))
	}
	if err := r.window(recommend.ScenarioReorder, &q); err != nil {
		return err
	}
	return r.period(recommend.ScenarioVisits, &q)
}

// scenarioID reads one id of "for": "" when it is not given.
func scenarioID(field string, v any) (string, error) {
	if v == nil {
		return "", nil
	}
	s, ok := recommend.ID(v)
	if !ok {
		return "", badRecommend(`"for.%s" is an id: a non-empty string or a number`, field)
	}
	return s, nil
}

// subjects reads "for" (and the top-level territory) into q.
func (r ScenarioRequest) subjects(q *recommend.Query) error {
	territory := r.For.Territory
	if territory == nil {
		territory = r.Territory
	}
	for _, f := range []struct {
		name string
		v    any
		to   *string
	}{{"customer", r.For.Customer, &q.Customer}, {"product", r.For.Product, &q.Product},
		{"salesman", r.For.Salesman, &q.Salesman}, {"territory", territory, &q.Territory}} {
		s, err := scenarioID(f.name, f.v)
		if err != nil {
			return err
		}
		*f.to = s
	}
	q.AllCustomers = r.For.AllCustomers
	for _, f := range []struct {
		name, what string
		v          any
		to         *string
	}{{"segment", "customer category", r.For.Segment, &q.Segment}, {"size", "customer size", r.For.Size, &q.Size},
		{"price_tier", "customer price tier", r.For.PriceTier, &q.PriceTier}, {"channel", "customer channel", r.For.Channel, &q.Channel},
		{"region", "customer region", r.For.Region, &q.Region},
		{"brand", "product brand", r.For.Brand, &q.Brand}, {"subcategory", "product subcategory", r.For.Subcategory, &q.Subcategory}} {
		if f.v == nil {
			continue
		}
		s, ok := recommend.ID(f.v)
		if _, isBool := f.v.(bool); !ok || isBool {
			return badRecommend(`"for.%s" is a %s: a non-empty string or a number`, f.name, f.what)
		}
		*f.to = s
	}
	if len(r.For.Customers) > recommend.MaxBatch {
		return badRecommend("at most %d customers per call, not %d", recommend.MaxBatch, len(r.For.Customers))
	}
	for _, c := range r.For.Customers {
		s, err := scenarioID("customers", c)
		if err != nil || s == "" {
			return badRecommend(`"for.customers" lists ids: non-empty strings or numbers`)
		}
		q.Customers = append(q.Customers, s)
	}
	return nil
}

// period reads a visit plan's "period" into q.
func (r ScenarioRequest) period(scenario string, q *recommend.Query) error {
	if r.Period == nil {
		return nil
	}
	if scenario != recommend.ScenarioVisits {
		return badRecommend(`"period" applies to a visit plan only`)
	}
	from, err := time.Parse(time.DateOnly, r.Period.From)
	if err != nil {
		return badRecommend(`"period.from" is a date, YYYY-MM-DD`)
	}
	q.From = from
	if r.Period.To != "" {
		to, err := time.Parse(time.DateOnly, r.Period.To)
		if err != nil {
			return badRecommend(`"period.to" is a date, YYYY-MM-DD`)
		}
		q.To = to
	}
	return nil
}

// RecommendScenario answers a call of a scenario model in-process from its
// live version's artifact. Every subject (each customer of a batch) counts as
// one row against the vhost's predictions-per-second quota, and the call is
// logged and monitored as a prediction carrying its recommendation_id.
func (s *Service) RecommendScenario(ctx context.Context, vhost, name string, req ScenarioRequest) (ScenarioAnswer, error) {
	m, err := s.Model(ctx, vhost, name)
	if err != nil {
		return ScenarioAnswer{}, err
	}
	if !m.IsRecommender() {
		return ScenarioAnswer{}, fmt.Errorf("model %q: %w", name, ErrNotRecommender)
	}
	quotas, err := s.Quotas(ctx, vhost)
	if err != nil {
		return ScenarioAnswer{}, err
	}
	start := time.Now()
	rec, err := s.recommender(ctx, m)
	if err != nil {
		observe(vhost, name, 1, time.Since(start), err)
		return ScenarioAnswer{}, fmt.Errorf("model %q: %w", name, err)
	}
	q, rows, err := req.query(rec.Scenario(), s.now())
	if err != nil {
		return ScenarioAnswer{}, err
	}
	if err := allowPredictions(vhost, quotas.MaxPredictionsPerSecond, rows, s.now()); err != nil {
		return ScenarioAnswer{}, err
	}
	results, err := rec.Answer(q)
	if err != nil {
		if errors.Is(err, recommend.ErrBadQuery) || errors.Is(err, recommend.ErrBadRule) || errors.Is(err, recommend.ErrLoosens) {
			return ScenarioAnswer{}, fmt.Errorf("%w: %w", ErrBadRecommendRequest, err)
		}
		return ScenarioAnswer{}, err
	}
	took := time.Since(start)
	observe(vhost, name, rows, took, nil)
	out := ScenarioAnswer{RecommendationID: newRecommendationID(), Results: results}
	s.watchScenario(ctx, m, rec, q, out, start, took)
	recommendationsTotal.WithLabelValues(vhost, name, "scenario_"+rec.Scenario(), "model").Inc()
	return out, nil
}

// watchScenario hands a scenario call to the monitor, logged like a
// prediction: the request and its recommendation_id as the input, one output
// per result. A products or similar call of one subject also counts for the
// model's coverage and popular share, as a #268 call does.
func (s *Service) watchScenario(ctx context.Context, m storage.MLModel, rec *recommend.Model, q recommend.Query, out ScenarioAnswer,
	start time.Time, took time.Duration) {
	input := map[string]any{"recommendation_id": out.RecommendationID, "scenario": rec.Scenario(), "k": q.K}
	for key, v := range map[string]string{"customer": q.Customer, "product": q.Product, "salesman": q.Salesman, "territory": q.Territory} {
		if v != "" {
			input[key] = v
		}
	}
	if len(q.Customers) > 0 {
		input["customers"] = q.Customers
	}
	if len(q.Rules) > 0 {
		input["rules"] = len(q.Rules)
	}
	outputs := make([]map[string]any, len(out.Results))
	for i, r := range out.Results {
		ids := make([]any, len(r.Items))
		for j, p := range r.Items {
			ids[j] = p.ID
		}
		outputs[i] = map[string]any{"for": r.For, "items": ids}
	}
	var rc *monitor.RecommendationCall
	scenario := rec.Scenario()
	if len(out.Results) == 1 && (scenario == recommend.ScenarioProducts || scenario == recommend.ScenarioSimilar) {
		r := out.Results[0]
		ids := make([]string, len(r.Items))
		fallback := len(r.Items) > 0
		for j, p := range r.Items {
			ids[j] = p.ID
			if len(p.Reasons) == 0 || p.Reasons[0].Code != recommend.ReasonPopular {
				fallback = false
			}
		}
		rc = &monitor.RecommendationCall{Items: ids, Fallback: fallback, Catalog: rec.Items()}
	}
	c := callerOf(ctx)
	s.monitor.Observe(monitor.Observation{
		Model: m, CallerKind: c.kind, CallerID: c.id, Latency: took, At: start,
		Inputs: []map[string]any{input}, Outputs: outputs, Recommendation: rc,
	})
}

// RecommendWithID is Recommend, and the recommendation_id of its answer, for
// feedback.
func (s *Service) RecommendWithID(ctx context.Context, vhost, name string, req RecommendRequest) ([]recommend.Item, string, error) {
	id := newRecommendationID()
	items, err := s.recommend(ctx, vhost, name, req, id)
	if err != nil {
		return nil, "", err
	}
	return items, id, nil
}
