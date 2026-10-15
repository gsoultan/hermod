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
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/gsoultan/hermod/pkg/ml/builtin/learn"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// Auto's presets: the candidates in the order they are tried, the folds
// (1 for one validation split of validationFraction) and the time budget.
// Unlike the worker's best preset, none searches the candidates' settings.
type preset struct {
	algorithms []string
	folds      int
	budget     time.Duration
}

var presets = map[string]preset{
	"quick":    {[]string{learn.AlgorithmGradientBoosting, learn.AlgorithmLinear}, 1, 30 * time.Second},
	"balanced": {[]string{learn.AlgorithmGradientBoosting, learn.AlgorithmRandomForest, learn.AlgorithmLinear}, 3, 120 * time.Second},
	"best": {[]string{learn.AlgorithmGradientBoosting, learn.AlgorithmRandomForest, learn.AlgorithmLinear, learn.AlgorithmMLP},
		5, 600 * time.Second},
}

const validationFraction = 0.25

// fitFunc fits one algorithm on some rows, with the training's settings.
type fitFunc func(ctx context.Context, algorithm string, x *learn.Frame, y []float64) (*learn.Model, error)

// errOutOfTime ends a candidate that the budget ran out on.
var errOutOfTime = errors.New("out of time")

// autoRun is one auto choice: the rows, the folds and what was found.
type autoRun struct {
	task     string
	k        int
	x        *learn.Frame
	y        []float64
	folds    []learn.Fold
	fit      fitFunc
	deadline time.Time

	board   []worker.LeaderboardEntry
	notes   []string
	skipped []string
}

// choose scores every candidate of the preset on the training rows only, by
// cross-validation, and returns the best with the leaderboard (selection.py
// select). The first candidate always finishes; after it, none is started,
// or left running, once the budget is spent.
func choose(ctx context.Context, presetName, task string, k int, x *learn.Frame, y []float64, seed uint64,
	fit fitFunc) (string, []worker.LeaderboardEntry, []string, error) {
	p := presets[presetName]
	run := &autoRun{task: task, k: k, x: x, y: y, fit: fit, folds: autoFolds(p, task, x.N, y, seed),
		deadline: time.Now().Add(p.budget)}
	for _, alg := range p.algorithms {
		if err := run.try(ctx, alg); err != nil {
			return "", nil, nil, err
		}
	}
	if len(run.skipped) > 0 {
		run.notes = append(run.notes, fmt.Sprintf("Auto ran out of its %g-second time budget and did not try: %s.",
			p.budget.Seconds(), strings.Join(run.skipped, ", ")))
	}
	if len(run.board) == 0 {
		run.notes = append(run.notes, "Auto could not score any candidate, so it trained a random forest.")
		return learn.AlgorithmRandomForest, nil, run.notes, nil
	}
	// Best first; a tie keeps the order the candidates were tried in.
	slices.SortStableFunc(run.board, func(a, b worker.LeaderboardEntry) int {
		switch {
		case a.Score > b.Score:
			return -1
		case a.Score < b.Score:
			return 1
		}
		return 0
	})
	run.board[0].Chosen = true
	return run.board[0].Algorithm, run.board, run.notes, nil
}

// autoFolds are the rows each fold fits on and scores: one validation split,
// or k folds, stratified by class where every class allows.
func autoFolds(p preset, task string, n int, y []float64, seed uint64) []learn.Fold {
	var byClass []float64
	if task == learn.TaskClassification {
		byClass = y
	}
	if p.folds <= 1 {
		f, s := learn.Split(n, byClass, validationFraction, seed)
		return []learn.Fold{{Fit: f, Score: s}}
	}
	return learn.Folds(n, byClass, p.folds, seed)
}

// try scores one candidate and records it, or why it was left out. Only the
// training's own context ending is an error.
func (r *autoRun) try(ctx context.Context, alg string) error {
	began := time.Now()
	cctx, cancel := ctx, context.CancelFunc(func() {})
	if len(r.board) > 0 {
		cctx, cancel = context.WithDeadline(ctx, r.deadline)
	}
	scores, err := r.score(cctx, alg, len(r.board) > 0 && time.Now().After(r.deadline))
	cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	switch {
	case errors.Is(err, errOutOfTime), errors.Is(err, context.DeadlineExceeded):
		r.skipped = append(r.skipped, alg)
		return nil
	case err != nil:
		r.notes = append(r.notes, fmt.Sprintf("Auto skipped %s: it could not be trained on this data (%v).", alg, err))
		return nil
	}
	score := mean(scores)
	if math.IsNaN(score) || math.IsInf(score, 0) {
		r.notes = append(r.notes, fmt.Sprintf("Auto skipped %s: its score could not be measured on this data.", alg))
		return nil
	}
	metric := "r2"
	if r.task == learn.TaskClassification {
		metric = "balanced_accuracy"
	}
	r.board = append(r.board, worker.LeaderboardEntry{Algorithm: alg, Score: score, Metric: metric, Folds: len(r.folds),
		Seconds: math.Round(time.Since(began).Seconds()*1000) / 1000})
	return nil
}

// score is a candidate's score on each fold.
func (r *autoRun) score(ctx context.Context, alg string, late bool) ([]float64, error) {
	if late {
		return nil, errOutOfTime
	}
	var scores []float64
	for _, f := range r.folds {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m, err := r.fit(ctx, alg, r.x.Take(f.Fit), pick(r.y, f.Fit))
		if err != nil {
			return nil, err
		}
		scores = append(scores, judge(r.task, r.k, pick(r.y, f.Score), m.Predict(r.x.Take(f.Score))))
	}
	return scores, nil
}
