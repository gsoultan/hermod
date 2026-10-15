// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
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
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/gsoultan/hermod/pkg/ml/builtin/learn"
	"github.com/gsoultan/hermod/pkg/ml/builtin/table"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// spec reads and checks a training body.
func (e *Engine) spec(r *http.Request) (any, error) {
	raw, err := e.body(r)
	if err != nil {
		return nil, err
	}
	return parseSpec(raw)
}

// train answers POST /v1/models/{vhost}/{name}/train: it checks the spec,
// takes a training slot (or answers busy) and trains in the request's
// context, so a caller that gives up, or Hermod's train timeout, stops it.
func (e *Engine) train(r *http.Request) (any, error) {
	vhost, name := r.PathValue("vhost"), r.PathValue("name")
	if err := checkModel(vhost, name); err != nil {
		return nil, err
	}
	spec, err := e.spec(r)
	if err != nil {
		return nil, err
	}
	release, err := e.slot()
	if err != nil {
		return nil, err
	}
	defer release()
	ctx, cancel := trainContext(r.Context())
	defer cancel()
	switch req := spec.(type) {
	case *recommendRequest:
		return e.trainRecommender(ctx, vhost, name, req)
	case *trainRequest:
		return e.trainTabular(ctx, vhost, name, req)
	}
	return nil, fmt.Errorf("unexpected training spec %T", spec)
}

// prepared is a dataset read for a training of a column: the usable rows
// (those with a target value), the features, the task and the target as a
// model learns it.
type prepared struct {
	all      *table.Table
	usable   *table.Table
	features []string
	task     string
	target   target
}

// prepare reads the dataset for req and works out the task and target, as
// a training and its pre-flight check both do.
func (e *Engine) prepare(ctx context.Context, vhost string, req *trainRequest) (*prepared, error) {
	t, err := e.datasets.load(ctx, vhost, req.dataset, 0)
	if err != nil {
		return nil, err
	}
	tc, features, err := columns(t, req.dataset, req.target, req.features)
	if err != nil {
		return nil, err
	}
	p := &prepared{all: t, usable: t.Take(usableRows(t, tc)), features: features}
	ut := p.usable.Column(req.target)
	p.task = detectTask(req.task, ut)
	if p.target, err = targetValues(p.task, req.target, ut); err != nil {
		return nil, err
	}
	return p, nil
}

// trainCheck answers POST /v1/train-check/{vhost}: what a training would make
// of the dataset, without training anything or taking a slot.
func (e *Engine) trainCheck(r *http.Request) (any, error) {
	vhost := r.PathValue("vhost")
	if err := checkName("vhost", vhost); err != nil {
		return nil, err
	}
	spec, err := e.spec(r)
	if err != nil {
		return nil, err
	}
	req, ok := spec.(*trainRequest)
	if !ok {
		return nil, badRequest("The pre-flight check is for a model that predicts a column; a recommendation model has none to check, so train it directly.")
	}
	p, err := e.prepare(r.Context(), vhost, req)
	if err != nil {
		return nil, err
	}
	warnings, ok := targetWarnings(p.task, req.target, p.target)
	out := worker.TrainCheck{Task: p.task, TargetKind: "number", Rows: p.all.Rows, UsableRows: p.usable.Rows, OK: ok}
	if p.task == learn.TaskClassification {
		k := p.target.classes()
		out.Classes, out.TargetKind = &k, "category"
		if k == 2 {
			out.TargetKind = "yes_no"
		}
	}
	cols, notes := p.columnChecks(req)
	out.Columns = cols
	out.Warnings = append(append([]worker.CheckWarning{}, warnings...), notes...)
	return out, nil
}

// columnChecks are every column's role, the target's too, in the dataset's
// order, and the warnings for the features not to use.
func (p *prepared) columnChecks(req *trainRequest) ([]worker.ColumnCheck, []worker.CheckWarning) {
	var cols []worker.ColumnCheck
	var notes []worker.CheckWarning
	if p.usable.Rows > 0 && (p.task != learn.TaskClassification || p.target.classes() >= 2) {
		cols, notes = featureNotes(p.usable, p.features, p.task, p.target, req.seed)
	} else {
		for _, f := range p.features {
			cols = append(cols, worker.ColumnCheck{Name: f, Type: p.all.Column(f).Kind, Role: "use"})
		}
	}
	cols = append(cols, worker.ColumnCheck{Name: req.target, Type: p.all.Column(req.target).Kind, Role: "target",
		Note: "What the model learns to predict."})
	order := map[string]int{}
	for i, c := range p.all.Columns {
		order[c.Name] = i
	}
	slices.SortFunc(cols, func(a, b worker.ColumnCheck) int { return order[a.Name] - order[b.Name] })
	return cols, notes
}

// trainTabular trains a model that predicts a column, as the worker's
// Trainer._train does, and saves it as the model's next version.
func (e *Engine) trainTabular(ctx context.Context, vhost, name string, req *trainRequest) (worker.Version, error) {
	p, err := e.prepare(ctx, vhost, req)
	if err != nil {
		return worker.Version{}, err
	}
	if p.usable.Rows < minRows {
		return worker.Version{}, badRequest("Training needs at least %d rows with a target value; found %d.", minRows, p.usable.Rows)
	}
	if p.task == learn.TaskClassification && p.target.classes() < 2 {
		return worker.Version{}, badRequest("Classification needs at least two distinct values in %q.", req.target)
	}
	warnings := p.warnings(req)
	if note := p.limit(e.cfg.MaxTrainRows, req.seed); note != "" {
		warnings = append(warnings, note)
	}
	s, err := p.split(req)
	if err != nil {
		return worker.Version{}, err
	}
	fit := e.fitter(p.task, p.target.classes(), req)

	algorithm := req.algorithm
	var board []worker.LeaderboardEntry
	if algorithm == "auto" {
		var autoNotes []string
		algorithm, board, autoNotes, err = choose(ctx, req.preset, p.task, p.target.classes(), s.xTrain, s.yTrain, req.seed, fit)
		if err != nil {
			return worker.Version{}, err
		}
		warnings = append(warnings, autoNotes...)
	}
	model, err := fit(ctx, algorithm, s.xTrain, s.yTrain)
	if err != nil {
		return worker.Version{}, trainError(algorithm, err)
	}

	v := s.version(model, p, req, e.now())
	v.Algorithm, v.Warnings = algorithm, warnings
	if req.algorithm == "auto" {
		v.Preset, v.Leaderboard = req.preset, board
	}
	if err := ctx.Err(); err != nil {
		return worker.Version{}, err
	}
	raw, err := model.Encode()
	if err != nil {
		return worker.Version{}, badRequest("The trained model is too large to keep: %v. Use fewer features or rows, or the hermod-ml worker.", err)
	}
	return e.models.save(ctx, vhost, name, v, modelFile, raw)
}

// warnings are the data notes for a version, in words, measured on every
// usable row like the pre-flight check, so the two agree.
func (p *prepared) warnings(req *trainRequest) []string {
	notes, _ := targetWarnings(p.task, req.target, p.target)
	_, colNotes := featureNotes(p.usable, p.features, p.task, p.target, req.seed)
	var out []string
	for _, w := range append(notes, colNotes...) {
		out = append(out, w.Message)
	}
	return out
}

// limit samples the usable rows down to maxRows, and says so; "" when they
// fit.
func (p *prepared) limit(maxRows int, seed uint64) string {
	if p.usable.Rows <= maxRows {
		return ""
	}
	keep := sample(p.usable.Rows, maxRows, seed, 19)
	note := fmt.Sprintf("The dataset has %d usable rows; Hermod's built-in engine trained on a random sample of %d "+
		"(HERMOD_ML_BUILTIN_MAX_ROWS).", p.usable.Rows, maxRows)
	p.usable, p.target.y = p.usable.Take(keep), pick(p.target.y, keep)
	return note
}

// split is the features and target cut into the rows to train on and the
// rows held back to test on.
type split struct {
	trainRows, testRows int
	xTrain, xTest       *learn.Frame
	yTrain, yTest       []float64
	types               map[string]string
}

func (p *prepared) split(req *trainRequest) (*split, error) {
	x, types := featureFrame(p.usable, p.features)
	var byClass []float64
	if p.task == learn.TaskClassification {
		byClass = p.target.y
	}
	trainIdx, testIdx := learn.Split(x.N, byClass, req.testSize, req.seed)
	if len(trainIdx) == 0 || len(testIdx) == 0 {
		return nil, badRequest("Cannot split the data with test_size=%g: %d rows leave no rows to train or to test on.", req.testSize, x.N)
	}
	return &split{trainRows: len(trainIdx), testRows: len(testIdx), xTrain: x.Take(trainIdx), xTest: x.Take(testIdx),
		yTrain: pick(p.target.y, trainIdx), yTest: pick(p.target.y, testIdx), types: types}, nil
}

// fitter fits an algorithm with the training's settings: a lopsided
// classification weighs its classes equally, and the network takes the
// request's settings, or its defaults when auto picked it.
func (e *Engine) fitter(task string, k int, req *trainRequest) fitFunc {
	return func(ctx context.Context, algorithm string, x *learn.Frame, y []float64) (*learn.Model, error) {
		var w []float64
		if task == learn.TaskClassification && imbalanced(y, k) {
			w = balancedWeights(y, k)
		}
		params := req.params
		if algorithm == learn.AlgorithmMLP && params.Epochs == 0 {
			params, _ = learn.MLPParams{}.WithDefaults()
		}
		return learn.Fit(ctx, learn.Spec{Task: task, Algorithm: algorithm, Classes: k, Seed: req.seed,
			MLP: params, Workers: e.cfg.Workers}, x, y, w)
	}
}

// version describes a trained model: what it was trained on, and how it
// scored on the test rows.
func (s *split) version(model *learn.Model, p *prepared, req *trainRequest, now time.Time) worker.Version {
	k := p.target.classes()
	v := worker.Version{Task: p.task, Dataset: req.dataset, Target: req.target, Features: p.features,
		FeatureTypes: s.types, Fill: model.Prep.Fill, FeatureStats: featureStats(s.xTrain), Labels: p.target.labels,
		CreatedAt: now.UTC().Truncate(time.Second)}
	v.Rows.Train, v.Rows.Test = s.trainRows, s.testRows
	v.Metrics = scoreModel(p.task, k, model.Predict(s.xTest), model.Proba(s.xTest), s.yTest)
	v.Baseline = baseline(p.task, k, s.yTrain, s.yTest)
	v.Importance = importance(model, s.xTest, s.yTest, p.task, k, req.seed)
	v.Efficiency = efficiency(model.Net)
	return v
}

// efficiency is how a network was trained and is kept; nil for any other
// model.
func efficiency(n *learn.Net) *worker.Efficiency {
	if n == nil {
		return nil
	}
	ef := &worker.Efficiency{Requested: n.Params.Precision, Precision: n.Precision, Threads: n.Threads}
	if q := n.Quantization; q != nil {
		f32, i8 := q.Float32, q.Int8
		ef.Metric, ef.Float32, ef.Int8, ef.Tolerance = q.Metric, &f32, &i8, q.Tolerance
	}
	return ef
}
