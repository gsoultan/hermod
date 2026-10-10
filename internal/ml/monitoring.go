package ml

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/gsoultan/hermod/internal/ml/monitor"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

type callerKey struct{}

type caller struct{ kind, id string }

// WithCaller records who is calling Predict, for the prediction log: one of
// the storage.MLCaller kinds, and the workflow for a Predict node.
func WithCaller(ctx context.Context, kind, id string) context.Context {
	return context.WithValue(ctx, callerKey{}, caller{kind, id})
}

func callerOf(ctx context.Context) caller {
	c, _ := ctx.Value(callerKey{}).(caller)
	return c
}

// WithMonitor sets where predictions are reported; nil means nowhere.
func (s *Service) WithMonitor(m *monitor.Monitor) *Service {
	s.monitor = m
	return s
}

// WithLogStore sets the store prediction logs are read from and deleted in;
// without one they are the model store's.
func (s *Service) WithLogStore(logs func() any) *Service {
	s.logs = logs
	return s
}

func (s *Service) predictionLogs() (storage.MLPredictionLogStore, error) {
	from := s.logs
	if from == nil {
		from = s.store
	}
	ls, ok := from().(storage.MLPredictionLogStore)
	if !ok {
		return nil, storage.ErrMLPredictionLogsUnsupported
	}
	return ls, nil
}

// PredictionLogs returns a model's most recently logged predictions, newest
// first.
func (s *Service) PredictionLogs(ctx context.Context, vhost, name string, limit int) ([]storage.MLPredictionLog, error) {
	if _, err := s.Model(ctx, vhost, name); err != nil {
		return nil, err
	}
	ls, err := s.predictionLogs()
	if err != nil {
		return nil, err
	}
	logs, err := ls.ListMLPredictionLogs(ctx, vhost, name, storage.MLPredictionLogLimit(limit))
	if err != nil {
		return nil, err
	}
	if logs == nil {
		logs = []storage.MLPredictionLog{}
	}
	return logs, nil
}

// SetMonitoring replaces how a model is monitored, and nothing else of it.
func (s *Service) SetMonitoring(ctx context.Context, vhost, name string, mon storage.MLMonitoring, by string) (storage.MLModel, error) {
	if err := mon.Validate(); err != nil {
		return storage.MLModel{}, err
	}
	ms, err := s.Models()
	if err != nil {
		return storage.MLModel{}, err
	}
	m, err := s.Model(ctx, vhost, name)
	if err != nil {
		return storage.MLModel{}, err
	}
	m.Monitoring, m.UpdatedBy = mon, by
	if err := ms.PutMLModel(ctx, m); err != nil {
		return storage.MLModel{}, err
	}
	return m, nil
}

// DriftStatus is a model's latest drift report, or why it has none.
type DriftStatus struct {
	Report *monitor.Report `json:"report"`
	Reason string          `json:"reason,omitempty"`
	// Window and MinRows say when a report is made: every Window, for a
	// window of at least MinRows predicted rows.
	Window  string `json:"window,omitempty"`
	MinRows int    `json:"min_rows,omitempty"`
}

// Drift returns the model's latest drift report.
func (s *Service) Drift(ctx context.Context, vhost, name string) (DriftStatus, error) {
	m, err := s.Model(ctx, vhost, name)
	if err != nil {
		return DriftStatus{}, err
	}
	if s.monitor == nil {
		return DriftStatus{Reason: "drift is not measured by this Hermod process"}, nil
	}
	st := DriftStatus{Window: s.monitor.Window().String(), MinRows: s.monitor.MinRows()}
	if r, ok := s.monitor.Report(vhost, name); ok {
		st.Report = &r
		return st, nil
	}
	switch {
	case m.Backend != storage.MLBackendWorker:
		st.Reason = "drift is measured for models trained in Hermod, against the rows they were trained on"
	case m.RemoteVersion == "":
		st.Reason = "no version of this model is live yet"
	default:
		st.Reason = fmt.Sprintf("no report yet: one is made every %s once at least %d predictions have been made "+
			"(a version trained before Hermod recorded training statistics has none; train it again)",
			st.Window, st.MinRows)
	}
	return st, nil
}

// VersionStats returns the training stats of one version of a trained model.
// It is the monitor's StatsFunc.
func (s *Service) VersionStats(ctx context.Context, vhost, name, version string) (map[string]worker.FeatureStats, error) {
	vs, err := s.Versions(ctx, vhost, name)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(vs, func(v worker.Version) bool { return v.Version == version })
	if i < 0 {
		return nil, fmt.Errorf("%w: %q has no version %q", ErrVersionNotFound, name, version)
	}
	return vs[i].FeatureStats, nil
}

// forgetModel drops what is kept about a deleted model's predictions. The
// logs are best effort: retention removes what this misses.
func (s *Service) forgetModel(ctx context.Context, vhost, name string) error {
	s.monitor.Forget(vhost, name)
	ls, err := s.predictionLogs()
	if errors.Is(err, storage.ErrMLPredictionLogsUnsupported) {
		return nil
	}
	if err := ls.DeleteMLPredictionLogs(ctx, vhost, name); err != nil {
		return fmt.Errorf("model %q was removed, but its prediction log was not: %w", name, err)
	}
	return nil
}
