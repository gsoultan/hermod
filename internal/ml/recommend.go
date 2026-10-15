// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package ml

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gsoultan/hermod/internal/ml/monitor"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/recommend"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

var (
	// ErrRecommender is returned for rows sent to a recommendation model.
	ErrRecommender = errors.New("this is a recommendation model: call it with a user or an item, not rows")
	// ErrNotRecommender is returned for a user or an item sent to a model
	// that predicts a column.
	ErrNotRecommender = errors.New("this model predicts from rows: a user or an item applies only to a recommendation model")
	// ErrBadRecommendRequest marks a recommendation call that is wrong in
	// itself.
	ErrBadRecommendRequest = errors.New("invalid recommendation request")
)

// maxRecommenders bounds the recommenders held in memory, one per model. An
// artifact may be up to recommend.MaxArtifactBytes, so this is far below the
// scorers' bound.
const maxRecommenders = 32

var recommenders = &versionCache[*recommend.Model]{
	entries: map[string]*versionEntry[*recommend.Model]{}, max: maxRecommenders, lasting: recommenderLasting,
}

// recommenderLasting reports whether a failed load stays failed until the
// version changes: the artifact is not one, or the worker does not have it.
func recommenderLasting(err error) bool {
	return errors.Is(err, recommend.ErrInvalid) || errors.Is(err, worker.ErrNotFound)
}

// RecommendRequest is one call of a recommendation model: the items for a
// person (User), or the items like one item (Item). User and Item are ids
// as JSON gives them: a string, or a number for an id column that held
// numbers. K is how many items, recommend.DefaultK when zero and at most
// recommend.MaxK. ExcludeSeen, for a person, leaves out what they already
// have; it is true when not given.
type RecommendRequest struct {
	User        any   `json:"user,omitempty"`
	Item        any   `json:"item,omitempty"`
	K           int   `json:"k,omitempty"`
	ExcludeSeen *bool `json:"exclude_seen,omitempty"`
}

// check reads the request: whether it asks for a person's items, the id, k
// and exclude_seen.
func (r RecommendRequest) check() (forUser bool, id string, k int, excludeSeen bool, err error) {
	bad := func(msg string) error { return fmt.Errorf("%w: %s", ErrBadRecommendRequest, msg) }
	switch {
	case r.User != nil && r.Item != nil:
		return false, "", 0, false, bad(`give "user" or "item", not both`)
	case r.User == nil && r.Item == nil:
		return false, "", 0, false, bad(`give "user" for a person's items, or "item" for items like it`)
	case r.K < 0:
		return false, "", 0, false, bad(`"k" is how many items, at least 1`)
	case r.Item != nil && r.ExcludeSeen != nil:
		return false, "", 0, false, bad(`"exclude_seen" applies to a user's items, not similar items`)
	}
	forUser = r.User != nil
	raw := r.Item
	if forUser {
		raw = r.User
	}
	id, ok := recommend.ID(raw)
	if !ok {
		return false, "", 0, false, bad(`"user" and "item" are ids: a non-empty string or a number`)
	}
	return forUser, id, recommend.ClampK(r.K), r.ExcludeSeen == nil || *r.ExcludeSeen, nil
}

// Recommend calls a vhost's recommendation model, in-process from its live
// version's artifact. A person or item the model does not know gets the most
// popular items, marked recommend.ReasonPopular, so the answer is never
// empty. Every call counts as one row against the vhost's
// predictions-per-second quota, and is logged and monitored as a prediction.
func (s *Service) Recommend(ctx context.Context, vhost, name string, req RecommendRequest) ([]recommend.Item, error) {
	return s.recommend(ctx, vhost, name, req, newRecommendationID())
}

// recommend is Recommend with the recommendation_id its answer is logged
// with.
func (s *Service) recommend(ctx context.Context, vhost, name string, req RecommendRequest, id string) ([]recommend.Item, error) {
	m, err := s.Model(ctx, vhost, name)
	if err != nil {
		return nil, err
	}
	if !m.IsRecommender() {
		return nil, fmt.Errorf("model %q: %w", name, ErrNotRecommender)
	}
	forUser, subject, k, excludeSeen, err := req.check()
	if err != nil {
		return nil, err
	}
	q, err := s.Quotas(ctx, vhost)
	if err != nil {
		return nil, err
	}
	if err := allowPredictions(vhost, q.MaxPredictionsPerSecond, 1, s.now()); err != nil {
		return nil, err
	}

	start := time.Now()
	rec, err := s.recommender(ctx, m)
	if err != nil {
		observe(vhost, name, 1, time.Since(start), err)
		return nil, fmt.Errorf("model %q: %w", name, err)
	}
	var items []recommend.Item
	var known bool
	input := inference.Row{"k": k, "recommendation_id": id}
	if forUser {
		items, known = rec.ForUser(subject, k, excludeSeen)
		input["user"], input["exclude_seen"] = subject, excludeSeen
	} else {
		items, known = rec.Similar(subject, k)
		input["item"] = subject
	}
	took := time.Since(start)
	observe(vhost, name, 1, took, nil)
	countRecommendation(m, forUser, known)
	s.watchRecommendation(ctx, m, input, items, !known, rec.Items(), start, took)
	return items, nil
}

// watchRecommendation hands a call to the monitor: logged like a prediction,
// and counted for coverage and popular share.
func (s *Service) watchRecommendation(ctx context.Context, m storage.MLModel, input inference.Row, items []recommend.Item,
	fallback bool, catalog int, start time.Time, took time.Duration) {
	ids := make([]string, len(items))
	listed := make([]any, len(items))
	for i, it := range items {
		ids[i] = it.Item
		entry := map[string]any{"item": it.Item, "score": it.Score}
		if it.Reason != "" {
			entry["reason"] = it.Reason
		}
		listed[i] = entry
	}
	c := callerOf(ctx)
	s.monitor.Observe(monitor.Observation{
		Model: m, CallerKind: c.kind, CallerID: c.id, Latency: took, At: start,
		Inputs: []map[string]any{input}, Outputs: []map[string]any{{"items": listed}},
		Recommendation: &monitor.RecommendationCall{Items: ids, Fallback: fallback, Catalog: catalog},
	})
}

// recommender returns the live version's recommender, loading it once for
// every caller, as scorer does a graph.
func (s *Service) recommender(ctx context.Context, m storage.MLModel) (*recommend.Model, error) {
	if s.worker == nil {
		return nil, ErrNoWorker
	}
	if m.RemoteVersion == "" {
		return nil, ErrNoLiveVersion
	}
	id := scorerID{version: m.RemoteVersion, saved: m.UpdatedAt}
	e := recommenders.get(s.worker.URL+"\x00"+m.VHost+"\x00"+m.Name, id, func(e *versionEntry[*recommend.Model]) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), scorerLoadTimeout)
		defer cancel()
		raw, err := s.worker.RecommenderFile(ctx, m.VHost, m.Name, m.RemoteVersion)
		if err != nil {
			e.err = fmt.Errorf("reading the recommender of version %s: %w", m.RemoteVersion, err)
			return
		}
		e.value, e.err = recommend.Parse(raw)
	})
	select {
	case <-e.ready:
		return e.value, e.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// checkRecommendSpec refuses a recommendation training Hermod cannot run:
// one without its columns, or bound for a worker pool. A recommender trains
// on the main worker, which serves its artifact; no pool hands one back. It
// takes no preset either: auto always tries both methods, so there is no
// effort to choose, and the worker refuses one rather than ignore it.
func checkRecommendSpec(spec worker.TrainSpec) error {
	if spec.Task != worker.TaskRecommendation {
		switch {
		case spec.Recommend != nil:
			return fmt.Errorf("%w: recommend applies only to task %q", ErrBadTraining, worker.TaskRecommendation)
		case spec.Scenario != "" || spec.Data != nil || spec.Rules != nil || spec.Weights != nil:
			return fmt.Errorf("%w: scenario, data, weights and rules apply only to task %q", ErrBadTraining, worker.TaskRecommendation)
		}
		return nil
	}
	if isScenario(spec) {
		_, err := scenarioSpec(spec)
		return err
	}
	switch {
	case spec.Recommend == nil || spec.Recommend.User == "" || spec.Recommend.Item == "":
		return fmt.Errorf("%w: a recommendation model needs recommend.user and recommend.item columns", ErrBadTraining)
	case spec.Device == DeviceGPU:
		return fmt.Errorf("%w: a recommendation model trains on the CPU of the main worker", ErrBadTraining)
	case strings.HasPrefix(spec.Algorithm, CustomPrefix):
		return fmt.Errorf("%w: a recommendation model trains with als, item_similarity or auto, not a script", ErrBadTraining)
	case spec.Preset != "":
		return fmt.Errorf("%w: a preset does not apply to a recommendation model; auto always tries als and item_similarity", ErrBadTraining)
	}
	return nil
}

// describe is a new trained model's description.
func describe(v worker.Version) string {
	if v.Task == worker.TaskRecommendation && v.Scenario != "" && v.Recommend == nil {
		return fmt.Sprintf("Recommendation scenario %q, from dataset %s", v.Scenario, v.Dataset)
	}
	if v.Task == worker.TaskRecommendation && v.Recommend != nil {
		return fmt.Sprintf("Recommends %s to each %s, from dataset %s", v.Recommend.Item, v.Recommend.User, v.Dataset)
	}
	return fmt.Sprintf("Predicts %s from dataset %s", v.Target, v.Dataset)
}
