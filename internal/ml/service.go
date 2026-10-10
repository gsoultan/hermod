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
	"sync"
	"time"

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
	ErrNoWorker = errors.New("no ML worker is configured: set HERMOD_ML_WORKER_URL")
	// ErrNoLiveVersion is returned for a trained model none of whose versions
	// has been put live.
	ErrNoLiveVersion = errors.New("no version of this model is live yet")
	// ErrVersionNotFound is returned for a version the model does not have.
	ErrVersionNotFound = errors.New("model version not found")
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
}

// envWorker is the worker HERMOD_ML_WORKER_URL names, read once.
var envWorker = sync.OnceValue(worker.FromEnv)

// NewService builds a Service. store is called on every use, because setup and
// a database switch replace the store while Hermod runs. secrets answers a
// model's token secret for its vhost; it may be nil when no model uses one.
// client may be nil for the default.
func NewService(store func() any, sec secrets.ScopedManager, client *inference.Client) *Service {
	if client == nil {
		client = inference.NewClient(nil)
	}
	return &Service{store: store, secrets: sec, client: client, worker: envWorker(), pools: envPools()}
}

// WithWorker replaces the ML worker the environment names; nil means none.
func (s *Service) WithWorker(w *worker.Client) *Service {
	s.worker = w
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
func (s *Service) Predict(ctx context.Context, vhost, name string, rows []inference.Row) ([]inference.Row, error) {
	if len(rows) > MaxRowsPerCall {
		return nil, ErrTooManyRows
	}
	m, err := s.Model(ctx, vhost, name)
	if err != nil {
		return nil, err
	}
	target, err := s.target(ctx, m)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	out, err := s.client.Predict(ctx, target, rows)
	observe(vhost, name, len(rows), time.Since(start), err)
	if err != nil {
		return nil, fmt.Errorf("model %q: %w", name, err)
	}
	return out, nil
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
	return inference.Target{
		Backend: inference.BackendOIP, URL: s.worker.ServingURL(m.VHost),
		Model: m.Name, Version: m.RemoteVersion, Token: s.worker.Token,
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
	if s.secrets == nil {
		return "", fmt.Errorf("model %q needs secret %q, and no secret store is available", m.Name, m.TokenSecret)
	}
	v, err := s.secrets.GetScoped(ctx, m.VHost, m.TokenSecret)
	if err != nil {
		return "", fmt.Errorf("model %q: reading secret %q: %w", m.Name, m.TokenSecret, err)
	}
	if v == "" {
		return "", fmt.Errorf("model %q needs secret %q, which vhost %q does not hold", m.Name, m.TokenSecret, m.VHost)
	}
	return v, nil
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
