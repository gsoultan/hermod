// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package ml is Hermod's model registry and inference path: the models a
// vhost can call, and the one Predict every caller uses — the Predict node,
// the REST endpoints and the gRPC InferenceService.
package ml

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gsoultan/hermod/internal/ml/monitor"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/worker"
	"github.com/gsoultan/hermod/pkg/security/secrets"
)

// MaxRowsPerCall bounds one prediction call. A caller with more rows splits
// them; this keeps one request from holding a model server, or Hermod's
// memory, hostage.
const MaxRowsPerCall = 1000

var (
	// ErrModelNotFound is returned for a model the vhost does not hold.
	ErrModelNotFound = errors.New("model not found")
	// ErrTooManyRows is returned for a call over MaxRowsPerCall.
	ErrTooManyRows = fmt.Errorf("at most %d rows per call", MaxRowsPerCall)
	// ErrServingOff is returned when a model has no serving key.
	ErrServingOff = errors.New("serving is not enabled for this model")
	// ErrBadServingKey is returned when the key presented is not the model's.
	ErrBadServingKey = errors.New("invalid serving key")
	// ErrNoWorker is returned for training, datasets, or a trained model's
	// prediction when Hermod has no ML worker configured.
	ErrNoWorker = errors.New("no ML worker is connected: connect one on the Machine Learning page, " +
		"or set HERMOD_ML_WORKER_URL")
	// ErrNoLiveVersion is returned for a trained model none of whose versions
	// has been put live.
	ErrNoLiveVersion = errors.New("no version of this model is live yet")
	// ErrVersionNotFound is returned for a version the model does not have.
	ErrVersionNotFound = errors.New("model version not found")
	// ErrBadMimirRequest is returned when a mimir server cannot be asked as
	// requested: no address, or a key secret that is not a secret's name.
	ErrBadMimirRequest = errors.New("the mimir server request is incomplete")
	// ErrSourceNotFound is returned for a database source a dataset is read
	// from that the vhost does not hold: missing, or another vhost's, whose
	// credentials are not this vhost's to use.
	ErrSourceNotFound = errors.New("database source not found")
	// ErrSourceTableNotFound is returned for a table the database source
	// does not list.
	ErrSourceTableNotFound = errors.New("the database source has no table by that name")
)

// servingKeyPrefix marks a serving key so a leaked one is recognisable in a
// log or a secret scanner.
const servingKeyPrefix = "hml_"

// Service resolves models and calls them.
type Service struct {
	store   func() any
	secrets secrets.ScopedManager
	client  *inference.Client
	worker  *worker.Client
	pools   Pools

	// quotaDefaults are the server-wide quotas, for a vhost without its own.
	quotaDefaults storage.MLQuotas
	// now is the clock the prediction rate is measured by.
	now func() time.Time

	// monitor is told of every prediction, for the prediction log and drift;
	// logs is the store the prediction log is read from. Both are optional.
	monitor *monitor.Monitor
	logs    func() any

	// syncTimeout bounds a call that waits for its answer (Sync);
	// maxQueuedJobs bounds a vhost's queued jobs; checkCallback vets a job's
	// callback URL. Tests replace them.
	syncTimeout   time.Duration
	maxQueuedJobs int
	checkCallback func(string) error
}

// NewService builds a Service. store is called on every use, because setup and
// a database switch replace the store while Hermod runs. secrets answers a
// model's token secret for its vhost; it may be nil when no model uses one.
// client may be nil for the default. The ML worker is the one in force in
// worker.Default when it is built.
func NewService(store func() any, sec secrets.ScopedManager, client *inference.Client) *Service {
	if client == nil {
		client = inference.NewClient(nil)
	}
	w, _ := worker.Default.Current()
	return &Service{store: store, secrets: sec, client: client, worker: w, pools: envPools(),
		quotaDefaults: envQuotaDefaults(), now: time.Now,
		syncTimeout: envSyncTimeout(), maxQueuedJobs: envMaxQueuedJobs(), checkCallback: CheckCallbackURL}
}

// WithWorker replaces the ML worker in force; nil means none.
func (s *Service) WithWorker(w *worker.Client) *Service {
	s.worker = w
	return s
}

// WithClock replaces the clock a recommendation's "today" and the prediction
// rate are read from. Tests set it, so a fixture's dates do not age.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// WithWorkerProvider takes the ML worker in force in p instead of
// worker.Default's.
func (s *Service) WithWorkerProvider(p *worker.Provider) *Service {
	s.worker, _ = p.Current()
	return s
}

// Worker returns the configured ML worker, or ErrNoWorker.
func (s *Service) Worker() (*worker.Client, error) {
	if s.worker == nil {
		return nil, ErrNoWorker
	}
	return s.worker, nil
}

// Models returns the store's model registry, or ErrMLModelsUnsupported.
func (s *Service) Models() (storage.MLModelStore, error) {
	ms, ok := s.store().(storage.MLModelStore)
	if !ok {
		return nil, storage.ErrMLModelsUnsupported
	}
	return ms, nil
}

// Model returns one model of the vhost, or ErrModelNotFound.
func (s *Service) Model(ctx context.Context, vhost, name string) (storage.MLModel, error) {
	ms, err := s.Models()
	if err != nil {
		return storage.MLModel{}, err
	}
	m, err := ms.GetMLModel(ctx, vhost, name)
	if errors.Is(err, storage.ErrNotFound) {
		return storage.MLModel{}, fmt.Errorf("%w: %q in vhost %q", ErrModelNotFound, name, vhost)
	}
	return m, err
}

// Predict sends rows to the vhost's model and returns one prediction per row.
// Every row counts against the vhost's predictions-per-second quota.
func (s *Service) Predict(ctx context.Context, vhost, name string, rows []inference.Row) ([]inference.Row, error) {
	if len(rows) > MaxRowsPerCall {
		return nil, ErrTooManyRows
	}
	m, err := s.Model(ctx, vhost, name)
	if err != nil {
		return nil, err
	}
	if m.IsRecommender() {
		return nil, fmt.Errorf("model %q: %w", name, ErrRecommender)
	}
	q, err := s.Quotas(ctx, vhost)
	if err != nil {
		return nil, err
	}
	if err := allowPredictions(vhost, q.MaxPredictionsPerSecond, len(rows), s.now()); err != nil {
		return nil, err
	}
	if s.inProcessVersion(m) {
		// The in-process engine scores its own versions, always in-process.
		return s.predictLocal(ctx, s.local(), m, rows)
	}
	if m.Backend == storage.MLBackendWorker && m.Scoring == storage.MLScoringInProcess && len(rows) > 0 {
		start := time.Now()
		if out, ok := s.predictInProcess(ctx, m, rows); ok {
			took := time.Since(start)
			observe(vhost, name, len(rows), took, nil)
			s.observed(ctx, m, rows, out, start, took)
			return out, nil
		}
	}
	target, err := s.target(ctx, m)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	out, err := s.client.Predict(ctx, target, rows)
	took := time.Since(start)
	observe(vhost, name, len(rows), took, err)
	if err != nil {
		return nil, fmt.Errorf("model %q: %w", name, err)
	}
	s.observed(ctx, m, rows, out, start, took)
	return out, nil
}

// observed tells the monitor of a prediction, wherever it was scored, for
// the prediction log and drift.
func (s *Service) observed(ctx context.Context, m storage.MLModel, rows, out []inference.Row, start time.Time, took time.Duration) {
	c := callerOf(ctx)
	s.monitor.Observe(monitor.Observation{
		Model: m, CallerKind: c.kind, CallerID: c.id, Inputs: rows, Outputs: out, Latency: took, At: start,
	})
}

// PutModel saves a model's definition. A model the vhost does not hold yet
// counts against its model quota.
func (s *Service) PutModel(ctx context.Context, m storage.MLModel) error {
	ms, err := s.Models()
	if err != nil {
		return err
	}
	q, err := s.Quotas(ctx, m.VHost)
	if err != nil {
		return err
	}
	mu := creationLock(m.VHost)
	mu.Lock()
	defer mu.Unlock()
	if err := s.checkNewModel(ctx, m.VHost, m.Name, q.MaxModels); err != nil {
		return err
	}
	return ms.PutMLModel(ctx, m)
}

// SetMCPExposed offers the model as a tool on Hermod's MCP server, or stops
// offering it, and returns the model as saved.
func (s *Service) SetMCPExposed(ctx context.Context, vhost, name string, exposed bool, by string) (storage.MLModel, error) {
	ms, err := s.Models()
	if err != nil {
		return storage.MLModel{}, err
	}
	m, err := s.Model(ctx, vhost, name)
	if err != nil {
		return storage.MLModel{}, err
	}
	m.MCPExposed, m.UpdatedBy = exposed, by
	if err := ms.PutMLModel(ctx, m); err != nil {
		return storage.MLModel{}, err
	}
	return m, nil
}

// target is where the model is called. A model trained by Hermod is on the
// configured worker, at its live version, with the worker's token; any other
// is where its definition says, with the token its secret holds.
func (s *Service) target(ctx context.Context, m storage.MLModel) (inference.Target, error) {
	if m.Backend != storage.MLBackendWorker {
		token, err := s.token(ctx, m)
		if err != nil {
			return inference.Target{}, err
		}
		return m.Target(token), nil
	}
	if s.worker == nil {
		return inference.Target{}, ErrNoWorker
	}
	if m.RemoteVersion == "" {
		return inference.Target{}, fmt.Errorf("model %q: %w", m.Name, ErrNoLiveVersion)
	}
	remote := s.remote()
	if remote == nil {
		return inference.Target{}, needsWorker(m)
	}
	return inference.Target{
		Backend: inference.BackendOIP, URL: remote.ServingURL(m.VHost),
		Model: m.Name, Version: m.RemoteVersion, Token: remote.Token,
		Features: m.Features, Timeout: time.Duration(m.TimeoutMs) * time.Millisecond,
	}, nil
}

// token reads the model's bearer token from its vhost's secrets. A model that
// names a secret its vhost cannot answer is refused rather than called
// without one: an unauthenticated call would fail anyway, less clearly.
func (s *Service) token(ctx context.Context, m storage.MLModel) (string, error) {
	if m.TokenSecret == "" {
		return "", nil
	}
	v, err := s.secret(ctx, m.VHost, m.TokenSecret)
	if err != nil {
		return "", fmt.Errorf("model %q %w", m.Name, err)
	}
	return v, nil
}

// secret reads one of the vhost's secrets, and says which one is missing
// when it cannot.
func (s *Service) secret(ctx context.Context, vhost, name string) (string, error) {
	if s.secrets == nil {
		return "", fmt.Errorf("needs secret %q, and no secret store is available", name)
	}
	v, err := s.secrets.GetScoped(ctx, vhost, name)
	if err != nil {
		return "", fmt.Errorf("reading secret %q: %w", name, err)
	}
	if v == "" {
		return "", fmt.Errorf("needs secret %q, which vhost %q does not hold", name, vhost)
	}
	return v, nil
}

// ListMimirModels asks the mimir server at url which models the API key in
// the vhost's secret tokenSecret may see, for a person to pick one from when
// connecting a model. The key never leaves the server side.
func (s *Service) ListMimirModels(ctx context.Context, vhost, url, tokenSecret string) ([]inference.RemoteModel, error) {
	if strings.TrimSpace(url) == "" {
		return nil, fmt.Errorf("%w: enter the server's address", ErrBadMimirRequest)
	}
	if !storage.ValidVHostSecretName(tokenSecret) {
		return nil, fmt.Errorf("%w: %q is not the name of a secret", ErrBadMimirRequest, tokenSecret)
	}
	token, err := s.secret(ctx, vhost, tokenSecret)
	if err != nil {
		return nil, fmt.Errorf("%w: the mimir server %v", ErrBadMimirRequest, err)
	}
	return s.client.ListMimirModels(ctx, strings.TrimSpace(url), token)
}

// RotateServingKey makes a new serving key for the model, replacing any
// earlier one, and returns it. Only its hash is stored: this is the one time
// the key can be read.
func (s *Service) RotateServingKey(ctx context.Context, vhost, name string) (string, error) {
	ms, err := s.Models()
	if err != nil {
		return "", err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("making a serving key: %w", err)
	}
	key := servingKeyPrefix + base64.RawURLEncoding.EncodeToString(buf)
	if err := ms.SetMLModelServingKey(ctx, vhost, name, hashServingKey(key)); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return "", fmt.Errorf("%w: %q in vhost %q", ErrModelNotFound, name, vhost)
		}
		return "", err
	}
	return key, nil
}

// DisableServing removes the model's serving key.
func (s *Service) DisableServing(ctx context.Context, vhost, name string) error {
	ms, err := s.Models()
	if err != nil {
		return err
	}
	if err := ms.SetMLModelServingKey(ctx, vhost, name, ""); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("%w: %q in vhost %q", ErrModelNotFound, name, vhost)
		}
		return err
	}
	return nil
}

// AuthorizeServing checks the key an application presented for a model. The
// comparison is between hashes and in constant time.
func (s *Service) AuthorizeServing(ctx context.Context, vhost, name, key string) error {
	m, err := s.Model(ctx, vhost, name)
	if err != nil {
		return err
	}
	if m.ServingKeyHash == "" {
		return ErrServingOff
	}
	if subtle.ConstantTimeCompare([]byte(hashServingKey(key)), []byte(m.ServingKeyHash)) != 1 {
		return ErrBadServingKey
	}
	return nil
}

func hashServingKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}
