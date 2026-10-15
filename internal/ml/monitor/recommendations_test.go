// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package monitor

import (
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

func recommender(vhost, name string) storage.MLModel {
	m := trainedModel(vhost, name, storage.MLMonitoring{})
	m.Task = storage.MLTaskRecommendation
	return m
}

func recCall(m storage.MLModel, fallback bool, items ...string) Observation {
	o := observation(m, map[string]any{"user": "u1", "k": 3})
	o.Recommendation = &RecommendationCall{Items: items, Fallback: fallback, Catalog: 10}
	return o
}

// A recommender has no feature drift, even with features declared; its
// windows report coverage and popular share instead, on the same tick and
// the same minimum count.
func TestARecommenderIsWatchedByCoverageAndPopularShareNotDrift(t *testing.T) {
	stats := &trainedStats{stats: map[string]worker.FeatureStats{"age": ageStats}}
	m := newTestMonitor(t, &logStore{}, stats, nil)
	model := recommender("rec-v", "recs")

	for i := range 100 {
		m.Observe(recCall(model, i%4 == 0, fmt.Sprintf("i%d", i%5), fmt.Sprintf("i%d", (i+1)%5)))
		drain(t, m) // the queue holds fewer than 100
	}
	drain(t, m)
	m.evaluate(t.Context(), time.Now())

	if _, ok := m.Report("rec-v", "recs"); ok {
		t.Error("a recommender got a drift report")
	}
	if stats.asked != 0 {
		t.Errorf("training stats were asked for %d times for a recommender", stats.asked)
	}
	r, ok := m.Recommendations("rec-v", "recs")
	if !ok {
		t.Fatal("no recommendation report after a full window")
	}
	if r.Calls != 100 || r.Fallbacks != 25 || r.PopularShare != 0.25 || r.Recommended != 5 || r.Catalog != 10 ||
		r.Coverage != 0.5 || r.Version != "4" {
		t.Errorf("report = %+v", r)
	}
	if got := testutil.ToFloat64(recommendationCoverage.WithLabelValues("rec-v", "recs")); got != 0.5 {
		t.Errorf("hermod_ml_recommendation_coverage = %v, want 0.5", got)
	}
	if got := testutil.ToFloat64(recommendationPopularShare.WithLabelValues("rec-v", "recs")); got != 0.25 {
		t.Errorf("hermod_ml_recommendation_popular_share = %v, want 0.25", got)
	}

	// Below the minimum, the window waits; the last report stands.
	m.Observe(recCall(model, true, "i9"))
	drain(t, m)
	m.evaluate(t.Context(), time.Now())
	if r2, _ := m.Recommendations("rec-v", "recs"); r2.Calls != 100 {
		t.Errorf("a window of one call was judged: %+v", r2)
	}

	m.Forget("rec-v", "recs")
	if _, ok := m.Recommendations("rec-v", "recs"); ok {
		t.Error("Forget kept the recommendation report")
	}
}

// A call of a model that is not a recommender is never counted as one.
func TestOnlyARecommendersCallsAreCountedAsRecommendations(t *testing.T) {
	m := newTestMonitor(t, &logStore{}, nil, nil)
	m.Observe(recCall(trainedModel("v", "churn", storage.MLMonitoring{}), false, "i1"))
	drain(t, m)
	if len(m.recWindows) != 0 {
		t.Errorf("windows = %v", m.recWindows)
	}
}
