// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/gsoultan/hermod/pkg/ml/builtin/table"
	"github.com/gsoultan/hermod/pkg/ml/recommend"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// Recommendations, as the worker trains them (ml/worker/hermod_ml/recommend.py):
// interactions of people and items, an als or item_similarity model, held-out
// metrics against the popular-items baseline, and the format 1 artifact
// (model.rec) Hermod serves in-process with pkg/ml/recommend. The settings
// are the worker's.
const (
	recK               = 10
	minInteractions    = 10
	alsFactors         = 32
	alsRegularization  = 0.1
	alsAlpha           = 20.0
	alsIterations      = 10
	similarNeighbors   = 50
	popularItems       = 100
	maxEvalUsers       = 5000
	recommendSamples   = 3
	recommenderFormat  = recommend.FormatVersion
	recommenderMagic   = "HREC"
	emptyNeighbor      = math.MaxUint32
	algorithmALS       = recommend.AlgorithmALS
	algorithmSimilar   = recommend.AlgorithmItemSimilarity
	recommendScenarioP = "products"
)

// interactions are the distinct (person, item) pairs: indices into the
// sorted ids, the summed strength, and the latest time (nil without a time
// column).
type interactions struct {
	users, items []string
	user, item   []int
	strength     []float64
	time         []float64
}

// readInteractions reads the request's columns, dropping rows with no person
// or item, a strength that is missing or not above zero, or a missing time.
// The same pair seen again is one interaction: strengths add up and the
// latest time counts.
func readInteractions(t *table.Table, req *recommendRequest) (*interactions, error) {
	cols, err := recommendColumns(t, req)
	if err != nil {
		return nil, err
	}
	type pair struct {
		strength, time float64
	}
	pairs := map[[2]string]*pair{}
	var order [][2]string
	kept := 0
	for i := range t.Rows {
		key, s, tm, ok := cols.row(i)
		if !ok {
			continue
		}
		kept++
		if p, seen := pairs[key]; seen {
			p.strength += s
			p.time = max(p.time, tm)
			continue
		}
		pairs[key] = &pair{strength: s, time: tm}
		order = append(order, key)
	}
	if kept < minInteractions {
		return nil, badRequest("Training needs at least %d usable interactions; found %d.", minInteractions, kept)
	}
	in := &interactions{users: sortedIDs(order, 0), items: sortedIDs(order, 1)}
	if len(in.users) < 2 || len(in.items) < 2 {
		return nil, badRequest("Training needs at least two people and two items.")
	}
	for _, key := range order {
		u, _ := slices.BinarySearch(in.users, key[0])
		it, _ := slices.BinarySearch(in.items, key[1])
		in.user, in.item = append(in.user, u), append(in.item, it)
		in.strength = append(in.strength, pairs[key].strength)
		if cols.time != nil {
			in.time = append(in.time, pairs[key].time)
		}
	}
	return in, nil
}

func sortedIDs(pairs [][2]string, side int) []string {
	var ids []string
	for _, p := range pairs {
		ids = append(ids, p[side])
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

// recommendCols are the columns a recommender reads, the time as an
// ordering key per row.
type recommendCols struct {
	user, item, strength *table.Column
	time                 []float64
}

func recommendColumns(t *table.Table, req *recommendRequest) (*recommendCols, error) {
	c := &recommendCols{}
	for _, n := range []struct {
		key, name string
		col       **table.Column
	}{{"user", req.columns.User, &c.user}, {"item", req.columns.Item, &c.item}, {"strength", req.columns.Strength, &c.strength}} {
		if n.name == "" {
			continue
		}
		if *n.col = t.Column(n.name); *n.col == nil {
			return nil, badRequest("The %s column %q is not in dataset %q.", n.key, n.name, req.dataset)
		}
	}
	if c.strength != nil && c.strength.Kind != table.Number {
		return nil, badRequest("Strength column %q must hold numbers (a rating, quantity or count); it is %s.", req.columns.Strength, c.strength.Kind)
	}
	if req.columns.Time != "" {
		tc := t.Column(req.columns.Time)
		if tc == nil {
			return nil, badRequest("The time column %q is not in dataset %q.", req.columns.Time, req.dataset)
		}
		var err error
		if c.time, err = timeKeys(tc, req.columns.Time); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// row is row i's pair, strength and time, or false when the row is dropped.
func (c *recommendCols) row(i int) ([2]string, float64, float64, bool) {
	key := [2]string{c.user.Text(i), c.item.Text(i)}
	if key[0] == "" || key[1] == "" {
		return key, 0, 0, false
	}
	s := 1.0
	if c.strength != nil {
		s = c.strength.Num[i]
		if math.IsNaN(s) || math.IsInf(s, 0) || s <= 0 {
			return key, 0, 0, false
		}
	}
	tm := 0.0
	if c.time != nil {
		if tm = c.time[i]; math.IsNaN(tm) {
			return key, 0, 0, false
		}
	}
	return key, s, tm, true
}

// The date forms a time column may hold: ISO 8601, with or without a time
// or zone, as Excel cells and most exports write them.
var timeLayouts = []string{
	time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02 15:04",
	"2006-01-02", "2006/01/02", "2006/01/02 15:04:05",
}

// timeKeys orders rows by a time column: a number is its own key, a date its
// seconds since 1970; NaN where a value is missing or unreadable.
func timeKeys(c *table.Column, name string) ([]float64, error) {
	switch c.Kind {
	case table.Number:
		return c.Num, nil
	case table.Bool:
		return nil, badRequest("Time column %q holds true/false; it must hold dates or numbers.", name)
	}
	out := make([]float64, len(c.Str))
	parsed := false
	for i, s := range c.Str {
		out[i] = math.NaN()
		if c.Null[i] {
			continue
		}
		for _, layout := range timeLayouts {
			if tm, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
				out[i], parsed = float64(tm.UnixNano())/1e9, true
				break
			}
		}
	}
	if !parsed {
		return nil, badRequest("Time column %q holds no dates Hermod can read; use ISO 8601 dates or numbers.", name)
	}
	return out, nil
}

// holdout splits the interactions rows into the rows to train on and the
// rows to test on: each person with two or more holds back ceil(fraction *
// n), at most n - 1 — their latest with a time, else a random choice.
func (in *interactions) holdout(rows []int, fraction float64, seed uint64) (train, test []int) {
	byUser := map[int][]int{}
	var users []int
	for _, r := range rows {
		u := in.user[r]
		if byUser[u] == nil {
			users = append(users, u)
		}
		byUser[u] = append(byUser[u], r)
	}
	slices.Sort(users)
	rng := newRand(seed, 37)
	for _, u := range users {
		mine := byUser[u]
		n := len(mine)
		if n < 2 {
			continue
		}
		hold := min(n-1, max(1, int(math.Ceil(fraction*float64(n)))))
		if in.time != nil {
			// Latest last; ties by row, so the split repeats.
			slices.SortStableFunc(mine, func(a, b int) int {
				if in.time[a] != in.time[b] {
					return compareFloat(in.time[a], in.time[b])
				}
				return a - b
			})
			test = append(test, mine[n-hold:]...)
		} else {
			perm := rng.Perm(n)
			for _, p := range perm[:hold] {
				test = append(test, mine[p])
			}
		}
	}
	slices.Sort(test)
	for _, r := range rows {
		if _, found := slices.BinarySearch(test, r); !found {
			train = append(train, r)
		}
	}
	return train, test
}

func compareFloat(a, b float64) int {
	if a < b {
		return -1
	}
	return 1
}

// trainRecommender trains a recommendation model as the worker's
// recommend.train does, and saves its artifact as the model's next version.
func (e *Engine) trainRecommender(ctx context.Context, vhost, name string, req *recommendRequest) (worker.Version, error) {
	t, err := e.datasets.load(ctx, vhost, req.dataset, 0)
	if err != nil {
		return worker.Version{}, err
	}
	in, err := readInteractions(t, req)
	if err != nil {
		return worker.Version{}, err
	}
	all := make([]int, len(in.user))
	for i := range all {
		all[i] = i
	}
	trainRows, testRows := in.holdout(all, req.testSize, req.seed)
	if len(testRows) == 0 {
		return worker.Version{}, badRequest("Nobody has two or more interactions, so there is nothing to hold back and test on.")
	}
	fitter := recFitter{in: in, seed: req.seed, workers: e.cfg.Workers}
	algorithm, candidates := req.algorithm, map[string]float64(nil)
	if algorithm == "auto" {
		if algorithm, candidates, err = fitter.choose(ctx, trainRows, req); err != nil {
			return worker.Version{}, err
		}
	}
	truth := in.truths(testRows, req.seed)
	held, err := fitter.fit(ctx, algorithm, trainRows)
	if err != nil {
		return worker.Version{}, err
	}
	metrics, base := evaluate(held, in, truth)
	samples := sampleRecommendations(held, in, truth)

	final, err := fitter.fit(ctx, algorithm, nil)
	if err != nil {
		return worker.Version{}, err
	}
	artifact, err := final.artifact()
	if err != nil {
		return worker.Version{}, err
	}
	v := worker.Version{Task: worker.TaskRecommendation, Algorithm: algorithm, Dataset: req.dataset, Features: []string{},
		Metrics: metrics, Scenario: recommendScenarioP, Candidates: candidates, CreatedAt: e.now().UTC().Truncate(time.Second)}
	v.Rows.Train, v.Rows.Test = len(trainRows), len(testRows)
	if err := describeRecommender(&v, req, in, base, samples); err != nil {
		return worker.Version{}, err
	}
	return e.models.save(ctx, vhost, name, v, recommenderFile, artifact)
}

// artifact is the model's model.rec, refused when it is over the size limit
// Hermod serves, and checked to read back.
func (m *recModel) artifact() ([]byte, error) {
	raw, err := m.encode()
	if err != nil {
		return nil, err
	}
	if len(raw) > recommend.MaxArtifactBytes {
		return nil, badRequest("The trained recommender is %d MB, over the %d MB limit: it has %d people and %d items. "+
			"Train on fewer people or items.", len(raw)>>20, recommend.MaxArtifactBytes>>20, len(m.users), len(m.items))
	}
	if _, err := recommend.Parse(raw); err != nil {
		return nil, fmt.Errorf("the trained recommender does not read back: %w", err)
	}
	return raw, nil
}

// describeRecommender adds the columns, catalogue, baseline metrics, score
// and samples to a recommender's version.
func describeRecommender(v *worker.Version, req *recommendRequest, in *interactions, base map[string]float64, samples []recSample) error {
	cols := req.columns
	v.Recommend = &cols
	v.Catalog = &worker.Catalog{Users: len(in.users), Items: len(in.items)}
	for k, b := range base {
		v.Metrics["baseline_"+k] = b
	}
	v.Metrics["score"] = v.Metrics[ndcgKey]
	var err error
	v.Samples, err = json.Marshal(samples)
	return err
}

// truths are the items each test person really picked; at most
// maxEvalUsers people, sampled with the seed.
func (in *interactions) truths(test []int, seed uint64) map[int][]int {
	truth := map[int][]int{}
	for _, r := range test {
		truth[in.user[r]] = append(truth[in.user[r]], in.item[r])
	}
	if len(truth) <= maxEvalUsers {
		return truth
	}
	users := make([]int, 0, len(truth))
	for u := range truth {
		users = append(users, u)
	}
	slices.Sort(users)
	out := map[int][]int{}
	for _, i := range sample(len(users), maxEvalUsers, seed, 41) {
		out[users[i]] = truth[users[i]]
	}
	return out
}
