package ml

import (
	"context"
	"fmt"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// local is the in-process engine in force (Hermod's built-in one, when no
// hermod-ml worker is configured), or nil.
func (s *Service) local() worker.Local {
	if s.worker == nil {
		return nil
	}
	return s.worker.Local()
}

// checkEngine refuses, before anything is reserved, a training the
// in-process engine cannot run: an algorithm or a recommendation scenario
// only the hermod-ml worker trains. It is ErrNoWorker, since connecting a
// worker is the way to run it.
func (s *Service) checkEngine(ctx context.Context, spec worker.TrainSpec) error {
	if s.local() == nil {
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
