// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package builtin

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/gsoultan/hermod/pkg/ml/builtin/learn"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// The worker's defaults for a training (training.py).
const (
	defaultTestSize = 0.2
	defaultSeed     = 42
	defaultPreset   = "balanced"
)

// trainRequest is a checked training of a model that predicts a column.
type trainRequest struct {
	dataset   string
	target    string
	features  []string
	task      string
	algorithm string
	preset    string
	testSize  float64
	seed      uint64
	params    learn.MLPParams
}

// recommendRequest is a checked training of a recommendation model in the
// one-table shape (worker.RecommendSpec).
type recommendRequest struct {
	dataset   string
	columns   worker.RecommendSpec
	algorithm string
	testSize  float64
	seed      uint64
}

// rawSpec is a training body as Hermod sends it (worker.TrainSpec), with the
// numbers that have a default kept apart from a zero sent on purpose.
type rawSpec struct {
	worker.TrainSpec
	TestSize *float64            `json:"test_size"`
	Seed     *float64            `json:"seed"`
	Params   *worker.TrainParams `json:"params"`
}

// The algorithms the engine trains for a column, and for recommendations.
var (
	tabularAlgorithms   = []string{"auto", learn.AlgorithmRandomForest, learn.AlgorithmGradientBoosting, learn.AlgorithmLinear, learn.AlgorithmMLP}
	recommendAlgorithms = []string{"auto", "als", "item_similarity"}
	presetNames         = []string{"quick", "balanced", "best"}
	taskNames           = []string{"auto", "classification", "regression", worker.TaskRecommendation}
)

// parseSpec reads a training body: a trainRequest, or a recommendRequest
// for a recommendation model. What only the worker trains is refused with a
// message that says so.
func parseSpec(raw []byte) (any, error) {
	var s rawSpec
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, badRequest("The request body is not a valid training spec: %v", err)
	}
	testSize, seed, err := s.split()
	if err != nil {
		return nil, err
	}
	if s.Scenario != "" && s.Scenario != "products" || s.Data != nil {
		return nil, needsWorker("A recommendation scenario")
	}
	if err := checkDataset(s.Dataset); err != nil {
		return nil, err
	}
	if s.Task == worker.TaskRecommendation {
		return parseRecommend(s, testSize, seed)
	}
	req := &trainRequest{dataset: s.Dataset, target: s.Target, features: s.Features, task: or(s.Task, "auto"),
		algorithm: or(s.Algorithm, "auto"), testSize: testSize, seed: seed}
	if err := s.checkColumns(req.task); err != nil {
		return nil, err
	}
	if err := s.checkAlgorithm(req); err != nil {
		return nil, err
	}
	return req, nil
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// split is the test share and the seed, or their defaults.
func (s rawSpec) split() (float64, uint64, error) {
	testSize := defaultTestSize
	if s.TestSize != nil {
		testSize = *s.TestSize
		if !(testSize > 0 && testSize < 1) {
			return 0, 0, badRequest("'test_size' must be a number between 0 and 1 (exclusive).")
		}
	}
	seed := uint64(defaultSeed)
	if s.Seed != nil {
		v := *s.Seed
		if v != float64(int64(v)) || v < 0 || v >= 1<<32 {
			return 0, 0, badRequest("'seed' must be an integer between 0 and 4294967295.")
		}
		seed = uint64(v)
	}
	return testSize, seed, nil
}

func checkDataset(name string) error {
	if name == "" {
		return badRequest("'dataset' is required and must be a string.")
	}
	return checkName("dataset", name)
}

// checkColumns checks the target, the features and the task.
func (s rawSpec) checkColumns(task string) error {
	if s.Target == "" {
		return badRequest("'target' is required and must be a non-empty string.")
	}
	for i, f := range s.Features {
		if f == "" {
			return badRequest("'features' must be an array of column names.")
		}
		if slices.Contains(s.Features[:i], f) {
			return badRequest("'features' lists a column more than once.")
		}
	}
	if !slices.Contains(taskNames, task) {
		return badRequest("'task' must be one of %s.", strings.Join(taskNames, ", "))
	}
	if s.Recommend != nil {
		return badRequest("'recommend' applies only to task %q.", worker.TaskRecommendation)
	}
	return nil
}

// checkAlgorithm checks the algorithm, the network's settings and auto's
// preset, and fills them into req.
func (s rawSpec) checkAlgorithm(req *trainRequest) error {
	if slices.Contains(workerOnly, req.algorithm) {
		return needsWorker("Algorithm " + req.algorithm)
	}
	if !slices.Contains(tabularAlgorithms, req.algorithm) {
		return badRequest("'algorithm' must be one of %s.", strings.Join(tabularAlgorithms, ", "))
	}
	if req.algorithm == learn.AlgorithmMLP {
		var p learn.MLPParams
		if s.Params != nil {
			p = learn.MLPParams{HiddenLayers: s.Params.HiddenLayers, Epochs: s.Params.Epochs, BatchSize: s.Params.BatchSize,
				LearningRate: s.Params.LearningRate, Patience: s.Params.Patience, Precision: s.Params.Precision,
				Threads: s.Params.Threads}
		}
		var err error
		if req.params, err = p.WithDefaults(); err != nil {
			return badRequest("%s.", err)
		}
	} else if s.Params != nil {
		return badRequest("'params' applies only to the algorithms %s.", learn.AlgorithmMLP)
	}
	switch {
	case s.Preset != "" && !slices.Contains(presetNames, s.Preset):
		return badRequest("'preset' must be one of %s.", strings.Join(presetNames, ", "))
	case s.Preset != "" && req.algorithm != "auto":
		return badRequest("'preset' applies only when 'algorithm' is auto.")
	case s.Preset != "":
		req.preset = s.Preset
	case req.algorithm == "auto":
		req.preset = defaultPreset
	}
	return nil
}

// parseRecommend checks a recommendation training in the one-table shape,
// with the worker's words (recommend.py parse_request).
func parseRecommend(s rawSpec, testSize float64, seed uint64) (*recommendRequest, error) {
	switch {
	case s.Target != "":
		return nil, badRequest("'target' does not apply to a recommendation model; name the columns in 'recommend'.")
	case len(s.Features) > 0:
		return nil, badRequest("'features' does not apply to a recommendation model; it learns from 'recommend' columns only.")
	case s.Params != nil:
		return nil, badRequest("'params' does not apply to a recommendation model.")
	case s.Preset != "":
		return nil, badRequest("'preset' does not apply to a recommendation model; auto always tries als and item_similarity.")
	}
	if err := checkRecommendColumns(s.Recommend); err != nil {
		return nil, err
	}
	algorithm := or(s.Algorithm, "auto")
	if !slices.Contains(recommendAlgorithms, algorithm) {
		return nil, badRequest("A recommendation model's 'algorithm' must be one of %s.", strings.Join(recommendAlgorithms, ", "))
	}
	return &recommendRequest{dataset: s.Dataset, columns: *s.Recommend, algorithm: algorithm, testSize: testSize, seed: seed}, nil
}

func checkRecommendColumns(r *worker.RecommendSpec) error {
	switch {
	case r == nil:
		return badRequest("'recommend' is required for task recommendation: {\"user\": column, \"item\": column}.")
	case r.User == "":
		return badRequest("'recommend.user' must be a column name.")
	case r.Item == "":
		return badRequest("'recommend.item' must be a column name.")
	}
	named := []string{r.User, r.Item}
	for _, c := range []string{r.Strength, r.Time} {
		if c != "" {
			named = append(named, c)
		}
	}
	for i, c := range named {
		if slices.Contains(named[:i], c) {
			return badRequest("'recommend' names a column more than once; user, item, strength and time are different columns.")
		}
	}
	return nil
}
