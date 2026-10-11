package ml

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// What started a retraining.
const (
	TriggerSchedule = "schedule"
	TriggerNewRows  = "new_rows"
)

// ErrTrainingRunning is returned for a model another training holds, here or
// on another Hermod sharing the store.
var ErrTrainingRunning = errors.New("this model is already being trained")

// ErrNotTrainedHere is returned for a retrain policy on a model served
// elsewhere: Hermod cannot train it.
var ErrNotTrainedHere = errors.New("only a model trained here can retrain")

const (
	// RetrainInterval is how often a Retrainer looks for models that are due.
	// A schedule is therefore kept to within a minute.
	RetrainInterval = time.Minute
	// retryAfterFailure is how long a row trigger waits after a retraining
	// failed, so a dataset the model cannot train on is not tried every minute.
	retryAfterFailure = 15 * time.Minute
	// claimMargin is added to the worker's training timeout for a claim, so a
	// claim outlives the training it covers.
	claimMargin = 5 * time.Minute
	// retrainedBy is who a retraining is recorded as.
	retrainedBy = "retrain"
)

// cronParser reads the five-field schedules the cron source and the UI use,
// and descriptors such as @daily.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// ValidateRetrainPolicy reports what is wrong with a retrain policy.
func ValidateRetrainPolicy(p storage.MLRetrainPolicy) error {
	if p.Schedule == "" && p.NewRows == 0 {
		return errors.New("a retrain policy needs a schedule, a number of new rows, or both")
	}
	if p.NewRows < 0 {
		return errors.New("the number of new rows that retrains the model cannot be negative")
	}
	if p.Schedule != "" {
		if _, err := cronParser.Parse(p.Schedule); err != nil {
			return fmt.Errorf("schedule %q is not a five-field cron expression or a descriptor such as @daily: %w", p.Schedule, err)
		}
	}
	if p.Spec.Dataset == "" {
		return errors.New("name the dataset the model retrains on")
	}
	if p.Spec.Target == "" {
		return errors.New("name the target column the model predicts")
	}
	// The spec is a training's, device and custom script included; whether
	// their pools exist is checked when it trains, as for a training by hand.
	if err := checkDevice(p.Spec); err != nil {
		return err
	}
	return GoLive(p.GoLive).Validate()
}

// retrainStore is the store's MLRetrainStore, or ErrMLModelsUnsupported.
func (s *Service) retrainStore() (storage.MLRetrainStore, error) {
	rs, ok := s.store().(storage.MLRetrainStore)
	if !ok {
		return nil, storage.ErrMLModelsUnsupported
	}
	return rs, nil
}

// SetRetrainPolicy makes a trained model train again by itself, on a schedule,
// after its dataset grows, or both. It returns the model with its policy.
func (s *Service) SetRetrainPolicy(ctx context.Context, vhost, name string, p storage.MLRetrainPolicy, by string) (storage.MLModel, error) {
	if err := ValidateRetrainPolicy(p); err != nil {
		return storage.MLModel{}, err
	}
	m, err := s.Model(ctx, vhost, name)
	if err != nil {
		return storage.MLModel{}, err
	}
	if m.Backend != storage.MLBackendWorker {
		return storage.MLModel{}, fmt.Errorf("model %q is served elsewhere: %w", name, ErrNotTrainedHere)
	}
	rs, err := s.retrainStore()
	if err != nil {
		return storage.MLModel{}, err
	}
	p.UpdatedBy, p.UpdatedAt = by, time.Now().UTC()
	if err := rs.SetMLModelRetrain(ctx, vhost, name, &p); err != nil {
		return storage.MLModel{}, err
	}
	m.Retrain = &p
	return m, nil
}

// ClearRetrainPolicy stops a model retraining by itself. What its last
// retraining did stays on the model.
func (s *Service) ClearRetrainPolicy(ctx context.Context, vhost, name string) error {
	if _, err := s.Model(ctx, vhost, name); err != nil {
		return err
	}
	rs, err := s.retrainStore()
	if err != nil {
		return err
	}
	return rs.SetMLModelRetrain(ctx, vhost, name, nil)
}

// claimTraining claims the model for owner while it trains, and returns what
// releases the claim. A store without claims, and a model not registered yet,
// train unclaimed: there is nothing another training could be holding.
func (s *Service) claimTraining(ctx context.Context, vhost, name, owner string) (func(), error) {
	rs, ok := s.store().(storage.MLRetrainStore)
	if !ok {
		return func() {}, nil
	}
	ttl := worker.DefaultTrainTimeout
	if s.worker != nil && s.worker.TrainTimeout > 0 {
		ttl = s.worker.TrainTimeout
	}
	claimed, err := rs.ClaimMLModelTraining(ctx, vhost, name, owner, ttl+claimMargin)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return func() {}, nil
	case err != nil:
		return nil, err
	case !claimed:
		return nil, fmt.Errorf("%w: %q in vhost %q", ErrTrainingRunning, name, vhost)
	}
	return func() {
		// Released even when the caller's context ended, so the next training
		// does not wait for the claim to expire.
		_ = rs.ReleaseMLModelTraining(context.WithoutCancel(ctx), vhost, name, owner)
	}, nil
}

// recordTrainedRows notes how many rows the model's dataset held when it
// trained, which "after N new rows" counts from. The rest of the status, what
// the last retraining did, is kept.
func (s *Service) recordTrainedRows(ctx context.Context, vhost, name string, rows int) {
	rs, err := s.retrainStore()
	if err != nil {
		return
	}
	m, err := s.Model(ctx, vhost, name)
	if err != nil {
		return
	}
	var st storage.MLRetrainStatus
	if m.RetrainStatus != nil {
		st = *m.RetrainStatus
	}
	st.DatasetRows, st.TrainedAt = rows, time.Now().UTC()
	_ = rs.SetMLModelRetrainStatus(ctx, vhost, name, st)
}

// newClaimOwner names one training's claim: the host, for whoever reads the
// store, and random bytes, so two trainings in one process differ.
func newClaimOwner() string {
	host, _ := os.Hostname()
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return host + "-" + hex.EncodeToString(b)
}

// retrainDue says what makes the model retrain now: TriggerSchedule,
// TriggerNewRows, or "" for nothing. rows is how many rows its dataset holds
// now, or -1 when that is not known.
func retrainDue(p storage.MLRetrainPolicy, st *storage.MLRetrainStatus, rows int, now time.Time) string {
	if p.Schedule != "" {
		// The run after the last one, or after the policy was set: a run
		// missed while no Hermod was up happens once, not once per miss.
		basis := p.UpdatedAt
		if st != nil && st.At.After(basis) {
			basis = st.At
		}
		if sched, err := cronParser.Parse(p.Schedule); err == nil && !sched.Next(basis).After(now) {
			return TriggerSchedule
		}
	}
	if p.NewRows <= 0 || rows < 0 || st == nil {
		return ""
	}
	if st.Error != "" && now.Before(st.At.Add(retryAfterFailure)) {
		return ""
	}
	added := rows - st.DatasetRows
	if rows < st.DatasetRows {
		// The dataset was emptied or refilled: every row in it is new.
		added = rows
	}
	if added >= p.NewRows {
		return TriggerNewRows
	}
	return ""
}

// Retrainer retrains the models whose policies say so. Every Hermod serving
// the API runs one; a model's training claim makes each retraining happen on
// one of them, once.
type Retrainer struct {
	svc    func() *Service
	logger hermod.Logger
	owner  string
	now    func() time.Time
}

// NewRetrainer builds a Retrainer. svc is called on every round, because setup
// and a database switch replace the store while Hermod runs. logger may be
// nil.
func NewRetrainer(svc func() *Service, logger hermod.Logger) *Retrainer {
	return &Retrainer{svc: svc, logger: logger, owner: newClaimOwner(), now: time.Now}
}

// Run looks for due models every RetrainInterval until ctx ends.
func (r *Retrainer) Run(ctx context.Context) {
	t := time.NewTicker(RetrainInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.Tick(ctx)
		}
	}
}

// Tick retrains, one after another, every model that is due now.
func (r *Retrainer) Tick(ctx context.Context) {
	svc := r.svc()
	if svc == nil {
		return
	}
	if _, err := svc.Worker(); err != nil {
		return
	}
	rs, err := svc.retrainStore()
	if err != nil {
		return
	}
	models, err := rs.ListRetrainingMLModels(ctx)
	if err != nil {
		r.warn("Listing the models that retrain failed", "error", err)
		return
	}
	for _, m := range models {
		if ctx.Err() != nil {
			return
		}
		r.consider(ctx, svc, rs, m)
	}
}

// consider retrains m if it is due. m may be stale: the decision is made again
// once the model is claimed, from the store, so a retraining another replica
// finished meanwhile is not repeated.
func (r *Retrainer) consider(ctx context.Context, svc *Service, rs storage.MLRetrainStore, m storage.MLModel) {
	if m.Retrain == nil || m.Backend != storage.MLBackendWorker {
		return
	}
	trigger := r.due(ctx, svc, rs, m)
	if trigger == "" {
		return
	}
	release, err := svc.claimTraining(ctx, m.VHost, m.Name, r.owner)
	if err != nil {
		// Another training holds it; it is looked at again next round.
		return
	}
	defer release()

	m, err = svc.Model(ctx, m.VHost, m.Name)
	if err != nil || m.Retrain == nil {
		return
	}
	if trigger = r.due(ctx, svc, rs, m); trigger == "" {
		return
	}
	now := r.now().UTC()
	p := *m.Retrain
	res, err := svc.train(ctx, m.VHost, m.Name, p.Spec, GoLive(p.GoLive), retrainedBy)

	// train recorded the rows it saw; the rest of the status is this run's.
	var st storage.MLRetrainStatus
	if fresh, ferr := svc.Model(ctx, m.VHost, m.Name); ferr == nil && fresh.RetrainStatus != nil {
		st = *fresh.RetrainStatus
	} else if m.RetrainStatus != nil {
		st = *m.RetrainStatus
	}
	st.Trigger, st.Version, st.Live, st.Reason, st.Error = trigger, "", false, "", ""
	switch {
	case errors.Is(err, worker.ErrBusy):
		// The worker is training something else. The run stays due and is
		// tried again next round; At is left alone so the schedule says so.
		st.Error = err.Error()
	case err != nil:
		st.At, st.Error = now, err.Error()
		r.warn("Retraining a model failed", "vhost", m.VHost, "model", m.Name, "trigger", trigger, "error", err)
	default:
		st.At, st.Version, st.Live, st.Reason = now, res.Version.Version, res.Live, res.Reason
		r.info("Retrained a model", "vhost", m.VHost, "model", m.Name, "trigger", trigger,
			"version", res.Version.Version, "live", res.Live)
	}
	if err := rs.SetMLModelRetrainStatus(ctx, m.VHost, m.Name, st); err != nil {
		r.warn("Recording a retraining failed", "vhost", m.VHost, "model", m.Name, "error", err)
	}
}

// due is retrainDue with the dataset's row count read from the worker when the
// row trigger needs it. A policy with a row trigger and no count to start from
// records the current one: rows already there when it was set are not new.
func (r *Retrainer) due(ctx context.Context, svc *Service, rs storage.MLRetrainStore, m storage.MLModel) string {
	now := r.now().UTC()
	p := *m.Retrain
	if trigger := retrainDue(p, m.RetrainStatus, -1, now); trigger != "" || p.NewRows <= 0 {
		return trigger
	}
	w, err := svc.Worker()
	if err != nil {
		return ""
	}
	info, err := w.Dataset(ctx, m.VHost, p.Spec.Dataset)
	if err != nil {
		return ""
	}
	if m.RetrainStatus == nil {
		_ = rs.SetMLModelRetrainStatus(ctx, m.VHost, m.Name, storage.MLRetrainStatus{DatasetRows: info.Rows})
		return ""
	}
	return retrainDue(p, m.RetrainStatus, info.Rows, now)
}

func (r *Retrainer) info(msg string, kv ...any) {
	if r.logger != nil {
		r.logger.Info(msg, kv...)
	}
}

func (r *Retrainer) warn(msg string, kv ...any) {
	if r.logger != nil {
		r.logger.Warn(msg, kv...)
	}
}
