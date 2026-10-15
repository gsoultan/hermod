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
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// scenarioJobService is scenarioService over a SQLite store, so its calls can
// be queued and run as jobs.
func scenarioJobService(t *testing.T, fixture string) *Service {
	t.Helper()
	resetRecommenders()
	st := jobsStore(t)
	if err := st.(storage.MLModelStore).PutMLModel(t.Context(), trainedRecs()); err != nil {
		t.Fatal(err)
	}
	svc := NewService(func() any { return st }, nil, nil).WithWorker(scenarioWorker(t, fixture))
	svc.now = func() time.Time { return time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC) }
	return svc
}

// runScenarioJob queues sub, runs it and returns the finished job and its
// result.
func runScenarioJob(t *testing.T, svc *Service, sub JobSubmission) (storage.MLJob, json.RawMessage) {
	t.Helper()
	j, err := svc.SubmitJob(t.Context(), sub)
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	testRunner(svc, "r1", time.Now).Tick(t.Context())
	done, err := svc.Job(t.Context(), j.VHost, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := svc.JobResult(t.Context(), j.VHost, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	return done, raw
}

func TestAScenarioJobAnswersAsTheSyncCallDoes(t *testing.T) {
	svc := scenarioJobService(t, "visits")
	req := ScenarioRequest{For: ScenarioFor{Salesman: "R03"}, Period: &ScenarioPeriod{From: "2026-10-05", To: "2026-10-09"}, K: 4}
	sync, err := svc.RecommendScenario(t.Context(), "v", "recs", req)
	if err != nil {
		t.Fatal(err)
	}

	j, raw := runScenarioJob(t, svc, JobSubmission{VHost: "v", Model: "recs", Kind: storage.MLJobRecommend, Scenario: &req,
		CallerKind: storage.MLCallerREST})
	if j.Status != storage.MLJobSucceeded || j.Kind != storage.MLJobRecommend {
		t.Fatalf("job = %+v", j)
	}
	var async ScenarioAnswer
	if err := json.Unmarshal(raw, &async); err != nil {
		t.Fatalf("result %s: %v", raw, err)
	}
	// The same answer, with an id of its own for its feedback.
	if !strings.HasPrefix(async.RecommendationID, "rec_") || async.RecommendationID == sync.RecommendationID {
		t.Errorf("recommendation_id %q (sync %q)", async.RecommendationID, sync.RecommendationID)
	}
	want, _ := json.Marshal(sync.Results)
	got, _ := json.Marshal(async.Results)
	if string(got) != string(want) {
		t.Errorf("results\n got %s\nwant %s", got, want)
	}
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(raw, &keys)
	if len(keys) != 2 || keys["results"] == nil || keys["recommendation_id"] == nil {
		t.Errorf("the result is not the sync answer's shape: %s", raw)
	}
}

func TestAScenarioJobIsRefusedForWhatCouldNeverRun(t *testing.T) {
	svc := scenarioJobService(t, "visits")
	cases := []struct {
		name string
		sub  JobSubmission
		want error
	}{
		{"an id that is not one", JobSubmission{VHost: "v", Model: "recs", Kind: storage.MLJobRecommend,
			Scenario: &ScenarioRequest{For: ScenarioFor{Customer: map[string]any{}}}}, ErrBadRecommendRequest},
		{"a negative k", JobSubmission{VHost: "v", Model: "recs", Kind: storage.MLJobRecommend,
			Scenario: &ScenarioRequest{For: ScenarioFor{Salesman: "R03"}, K: -1}}, ErrBadRecommendRequest},
		{"a period that is not a date", JobSubmission{VHost: "v", Model: "recs", Kind: storage.MLJobRecommend,
			Scenario: &ScenarioRequest{For: ScenarioFor{Salesman: "R03"}, Period: &ScenarioPeriod{From: "05/10/2026"}}}, ErrBadRecommendRequest},
		{"an unknown model", JobSubmission{VHost: "v", Model: "nope", Kind: storage.MLJobRecommend,
			Scenario: &ScenarioRequest{For: ScenarioFor{Salesman: "R03"}}}, ErrModelNotFound},
		{"both kinds of call", JobSubmission{VHost: "v", Model: "recs", Kind: storage.MLJobRecommend,
			Recommend: &RecommendRequest{User: "C0001"}, Scenario: &ScenarioRequest{For: ScenarioFor{Salesman: "R03"}}}, ErrBadJob},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := svc.SubmitJob(t.Context(), c.sub); !errors.Is(err, c.want) {
				t.Errorf("SubmitJob = %v, want %v", err, c.want)
			}
		})
	}

	// What only the model's scenario can tell fails the job, in words.
	j, _ := runScenarioJob(t, svc, JobSubmission{VHost: "v", Model: "recs", Kind: storage.MLJobRecommend,
		Scenario: &ScenarioRequest{For: ScenarioFor{Customer: "C0001"}}})
	if j.Status != storage.MLJobFailed || !strings.Contains(j.Error, "salesman") {
		t.Errorf("a visit plan for a customer = %+v", j)
	}
}

func TestARecommendJobCarriesARecommendationID(t *testing.T) {
	svc := scenarioJobService(t, "products")
	j, raw := runScenarioJob(t, svc, JobSubmission{VHost: "v", Model: "recs", Kind: storage.MLJobRecommend,
		Recommend: &RecommendRequest{User: "C0001"}})
	var out struct {
		Model            string            `json:"model"`
		Items            []json.RawMessage `json:"items"`
		RecommendationID string            `json:"recommendation_id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || j.Status != storage.MLJobSucceeded {
		t.Fatalf("job = %+v result %s", j, raw)
	}
	if out.Model != "recs" || len(out.Items) == 0 || !strings.HasPrefix(out.RecommendationID, "rec_") {
		t.Errorf("result = %s", raw)
	}
}
