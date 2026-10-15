// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
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
	"fmt"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// local is the in-process engine in force (Hermod's built-in one, beside
// any hermod-ml worker), or nil.
func (s *Service) local() worker.Local {
	if s.worker == nil {
		return nil
	}
	return s.worker.Local()
}

// remote is the hermod-ml worker in force, or nil when none is connected.
func (s *Service) remote() *worker.Client {
	if s.worker == nil {
		return nil
	}
	return s.worker.Remote()
}

// inProcessVersion reports whether the model's live version is one the
// in-process engine trained, and so scores: the engine that trained a
// version always serves it, whichever engines are connected now.
func (s *Service) inProcessVersion(m storage.MLModel) bool {
	return s.local() != nil && m.Backend == storage.MLBackendWorker && worker.IsBuiltinVersion(m.RemoteVersion)
}

// needsWorker is the refusal of a model whose live version the hermod-ml
// worker trained, while no worker is connected: that model only.
func needsWorker(m storage.MLModel) error {
	return fmt.Errorf("model %q: version %s needs the hermod-ml worker, which trained it: %w", m.Name, m.RemoteVersion, ErrNoWorker)
}

// checkEngine refuses, before anything is reserved, a training no engine in
// force can run: an algorithm or a recommendation scenario only the
// hermod-ml worker trains, while only the in-process engine is connected.
// It is ErrNoWorker, since connecting a worker is the way to run it.
func (s *Service) checkEngine(ctx context.Context, spec worker.TrainSpec) error {
	if s.local() == nil || s.remote() != nil {
		return nil
	}
	if isScenario(spec) && (spec.Data != nil || spec.Scenario != "products") {
		return fmt.Errorf("%w: a recommendation scenario needs the hermod-ml worker", ErrNoWorker)
	}
	// The engine answers its capabilities in-process; were it not to, the
	// training itself would refuse the algorithm, in the engine's words.
	if caps, err := s.worker.Capabilities(ctx); err == nil {
		if reason, ok := caps.Unavailable[spec.Algorithm]; ok {
			return fmt.Errorf("%w: algorithm %s %s", ErrNoWorker, spec.Algorithm, reason)
		}
	}
	return nil
}

// predictLocal scores rows with the live version of a model the in-process
// engine trained.
func (s *Service) predictLocal(ctx context.Context, l worker.Local, m storage.MLModel, rows []inference.Row) ([]inference.Row, error) {
	if m.RemoteVersion == "" {
		return nil, fmt.Errorf("model %q: %w", m.Name, ErrNoLiveVersion)
	}
	start := time.Now()
	out, err := l.Predict(ctx, m.VHost, m.Name, m.RemoteVersion, rows)
	took := time.Since(start)
	observe(m.VHost, m.Name, len(rows), took, err)
	if err != nil {
		return nil, fmt.Errorf("model %q: %w", m.Name, err)
	}
	scoringTotal.WithLabelValues(m.VHost, m.Name, "in_process").Inc()
	s.observed(ctx, m, rows, out, start, took)
	return out, nil
}
