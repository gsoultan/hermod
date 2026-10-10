package ml

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// How a newly trained version is put live.
const (
	// GoLiveAlways puts every new version live.
	GoLiveAlways = "always"
	// GoLiveNever keeps every new version off; an Editor promotes one.
	GoLiveNever = "never"
	// GoLiveIf puts a version live when one of its metrics is within bounds.
	GoLiveIf = "if"
)

// GoLive says whether a newly trained version is put live. The zero value
// keeps it off, the safe choice for a caller that says nothing. It is the
// rule storage keeps with a retrain policy (storage.MLGoLive): Mode, and
// Metric with Min and Max for GoLiveIf, where an empty Metric means "score",
// which is accuracy for a classifier and R² for a regression.
type GoLive storage.MLGoLive

// Validate reports what is wrong with the rule.
func (g GoLive) Validate() error {
	switch g.Mode {
	case "", GoLiveAlways, GoLiveNever:
		return nil
	case GoLiveIf:
		if g.Min == nil && g.Max == nil {
			return errors.New(`a "go live if" rule needs a minimum, a maximum, or both`)
		}
		return nil
	}
	return fmt.Errorf("go live is %q, %q or %q, not %q", GoLiveAlways, GoLiveNever, GoLiveIf, g.Mode)
}

// decide says whether v goes live, and why, in words a person reads.
func (g GoLive) decide(v worker.Version) (bool, string) {
	switch g.Mode {
	case GoLiveAlways:
		return true, "every new version goes live"
	case GoLiveIf:
	default:
		return false, "new versions wait to be put live by hand"
	}
	metric := g.Metric
	if metric == "" {
		metric = "score"
	}
	got, ok := v.Metrics[metric]
	if !ok {
		return false, fmt.Sprintf("the model reports no %q metric", metric)
	}
	if g.Min != nil && got < *g.Min {
		return false, fmt.Sprintf("%s %.4g is below the minimum %.4g", metric, got, *g.Min)
	}
	if g.Max != nil && got > *g.Max {
		return false, fmt.Sprintf("%s %.4g is above the maximum %.4g", metric, got, *g.Max)
	}
	return true, fmt.Sprintf("%s %.4g is within bounds", metric, got)
}

// TrainResult is a finished training: the version made, and whether it is
// now the one the model serves.
type TrainResult struct {
	Model   storage.MLModel `json:"model"`
	Version worker.Version  `json:"version"`
	Live    bool            `json:"live"`
	Reason  string          `json:"reason"`
}

// Train trains a new version of a vhost's model on the worker and registers
// the model, if it is new. Whether the version goes live is up to rule; a
// version kept off stays on the worker, and Promote can put it live later.
//
// One training of a model runs at a time, across every Hermod sharing the
// store: while another holds the model, Train returns ErrTrainingRunning.
//
// The training takes one of the vhost's training slots, and a new model
// counts against its model quota.
func (s *Service) Train(ctx context.Context, vhost, name string, spec worker.TrainSpec, rule GoLive, by string) (TrainResult, error) {
	release, err := s.claimTraining(ctx, vhost, name, newClaimOwner())
	if err != nil {
		return TrainResult{}, err
	}
	defer release()
	return s.train(ctx, vhost, name, spec, rule, by)
}

// trainingSlot takes one of the vhost's training slots, refusing first a new
// model over the vhost's model quota: before the worker spends minutes on it.
// The model quota is checked again at registration, when another model may
// have taken the last place. done gives the slot back.
func (s *Service) trainingSlot(ctx context.Context, vhost, name string, isNew bool) (done func(), err error) {
	q, err := s.Quotas(ctx, vhost)
	if err != nil {
		return nil, err
	}
	if isNew {
		if err := s.checkNewModel(ctx, vhost, name, q.MaxModels); err != nil {
			return nil, err
		}
	}
	return startTraining(vhost, q.MaxConcurrentTrainings)
}

// register saves a trained model: a new one through PutModel, which holds it
// to the vhost's model quota, an existing one as it is.
func (s *Service) register(ctx context.Context, ms storage.MLModelStore, m storage.MLModel, isNew bool) error {
	if isNew {
		return s.PutModel(ctx, m)
	}
	return ms.PutMLModel(ctx, m)
}

// trainedModel is the model a training registers its version on: the
// vhost's model of that name, or a new one when it has none (isNew). A model
// of that name served elsewhere is refused.
func trainedModel(ctx context.Context, ms storage.MLModelStore, vhost, name string) (m storage.MLModel, isNew bool, err error) {
	m, err = ms.GetMLModel(ctx, vhost, name)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return storage.MLModel{VHost: vhost, Name: name, Backend: storage.MLBackendWorker}, true, nil
	case err != nil:
		return m, false, err
	case m.Backend != storage.MLBackendWorker:
		return m, false, fmt.Errorf("vhost %q already has a model %q served elsewhere; train under another name", vhost, name)
	}
	return m, false, nil
}

// train is Train for a caller that already holds the model's claim.
func (s *Service) train(ctx context.Context, vhost, name string, spec worker.TrainSpec, rule GoLive, by string) (TrainResult, error) {
	w, err := s.Worker()
	if err != nil {
		return TrainResult{}, err
	}
	if err := rule.Validate(); err != nil {
		return TrainResult{}, err
	}
	if !storage.ValidMLModelName(name) {
		return TrainResult{}, fmt.Errorf("model name %q must start with a letter and hold only letters, digits, '_' or '-'", name)
	}
	ms, err := s.Models()
	if err != nil {
		return TrainResult{}, err
	}
	m, isNew, err := trainedModel(ctx, ms, vhost, name)
	if err != nil {
		return TrainResult{}, err
	}
	done, err := s.trainingSlot(ctx, vhost, name, isNew)
	if err != nil {
		return TrainResult{}, err
	}
	defer done()

	// How many rows the training sees, for the next "after N new rows". Read
	// before it starts, so rows added while it runs count as new; a dataset
	// that cannot be read leaves the count where it was.
	rows, rowsErr := w.Dataset(ctx, vhost, spec.Dataset)

	v, err := w.Train(ctx, vhost, name, spec)
	if err != nil {
		return TrainResult{}, fmt.Errorf("training %q: %w", name, err)
	}
	live, reason := rule.decide(v)
	if live {
		m.RemoteVersion = v.Version
		m.Features, m.FeatureTypes = v.Features, v.FeatureTypes
	} else if m.RemoteVersion == "" {
		// Nothing is live yet, so the features the next promotion will serve
		// are the ones to show.
		m.Features, m.FeatureTypes = v.Features, v.FeatureTypes
	}
	if m.Description == "" {
		m.Description = fmt.Sprintf("Predicts %s from dataset %s", v.Target, v.Dataset)
	}
	m.UpdatedBy = by
	if err := s.register(ctx, ms, m, isNew); err != nil {
		return TrainResult{}, fmt.Errorf("version %s of %q was trained but not registered: %w", v.Version, name, err)
	}
	if rowsErr == nil {
		s.recordTrainedRows(ctx, vhost, name, rows.Rows)
	}
	return TrainResult{Model: m, Version: v, Live: live, Reason: reason}, nil
}

// Versions lists a trained model's versions, newest first.
func (s *Service) Versions(ctx context.Context, vhost, name string) ([]worker.Version, error) {
	m, err := s.Model(ctx, vhost, name)
	if err != nil {
		return nil, err
	}
	if m.Backend != storage.MLBackendWorker {
		return nil, fmt.Errorf("model %q is served elsewhere; its versions are kept there", name)
	}
	w, err := s.Worker()
	if err != nil {
		return nil, err
	}
	vs, err := w.Versions(ctx, vhost, name)
	if errors.Is(err, worker.ErrNotFound) {
		return nil, nil
	}
	return vs, err
}

// Promote puts one of a trained model's versions live: a later one, or an
// earlier one to roll back.
func (s *Service) Promote(ctx context.Context, vhost, name, version, by string) error {
	vs, err := s.Versions(ctx, vhost, name)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(vs, func(v worker.Version) bool { return v.Version == version })
	if i < 0 {
		return fmt.Errorf("%w: %q has no version %q", ErrVersionNotFound, name, version)
	}
	m, err := s.Model(ctx, vhost, name)
	if err != nil {
		return err
	}
	ms, err := s.Models()
	if err != nil {
		return err
	}
	m.RemoteVersion, m.Features, m.FeatureTypes, m.UpdatedBy = version, vs[i].Features, vs[i].FeatureTypes, by
	return ms.PutMLModel(ctx, m)
}

// DeleteModel removes a model from the registry and, for a model Hermod
// trained, its versions from the worker. The registry entry goes first, so a
// worker that cannot be reached leaves files behind, never a model that
// points at nothing.
func (s *Service) DeleteModel(ctx context.Context, vhost, name string) error {
	ms, err := s.Models()
	if err != nil {
		return err
	}
	m, err := s.Model(ctx, vhost, name)
	if err != nil {
		return err
	}
	if err := ms.DeleteMLModel(ctx, vhost, name); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("%w: %q in vhost %q", ErrModelNotFound, name, vhost)
		}
		return err
	}
	if m.Backend != storage.MLBackendWorker || s.worker == nil {
		return nil
	}
	if err := s.worker.DeleteModel(ctx, vhost, name); err != nil && !errors.Is(err, worker.ErrNotFound) {
		return fmt.Errorf("model %q was removed, but its files on the ML worker were not: %w", name, err)
	}
	return nil
}
