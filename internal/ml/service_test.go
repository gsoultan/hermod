package ml

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
)

// memStore is the MLModelStore part of a storage backend, in memory.
type memStore struct {
	storage.Storage
	mu     sync.Mutex
	models map[string]storage.MLModel
}

func newMemStore(models ...storage.MLModel) *memStore {
	s := &memStore{models: map[string]storage.MLModel{}}
	for _, m := range models {
		s.models[m.VHost+"/"+m.Name] = m
	}
	return s
}

func (s *memStore) ListMLModels(_ context.Context, vhost string) ([]storage.MLModel, error) {
	return nil, nil
}
func (s *memStore) GetMLModel(_ context.Context, vhost, name string) (storage.MLModel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.models[vhost+"/"+name]
	if !ok {
		return storage.MLModel{}, storage.ErrNotFound
	}
	return m, nil
}
func (s *memStore) PutMLModel(_ context.Context, m storage.MLModel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.models[m.VHost+"/"+m.Name]; ok {
		m.ServingKeyHash, m.Serving = old.ServingKeyHash, old.Serving
	}
	s.models[m.VHost+"/"+m.Name] = m
	return nil
}
func (s *memStore) SetMLModelServingKey(_ context.Context, vhost, name, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.models[vhost+"/"+name]
	if !ok {
		return storage.ErrNotFound
	}
	m.ServingKeyHash, m.Serving = hash, hash != ""
	s.models[vhost+"/"+name] = m
	return nil
}
func (s *memStore) DeleteMLModel(_ context.Context, vhost, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.models[vhost+"/"+name]; !ok {
		return storage.ErrNotFound
	}
	delete(s.models, vhost+"/"+name)
	return nil
}
func (s *memStore) DeleteMLModels(_ context.Context, vhost string) error      { return nil }

// secretsOf answers secrets from a map keyed "vhost/name".
type secretsOf map[string]string

func (m secretsOf) GetScoped(_ context.Context, vhost, key string) (string, error) {
	return m[vhost+"/"+key], nil
}

// mlflowEcho is an MLflow server that doubles x, and records the bearer token.
func mlflowEcho(t *testing.T, gotAuth *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotAuth = r.Header.Get("Authorization")
		var body struct {
			Records []map[string]float64 `json:"dataframe_records"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		preds := make([]float64, len(body.Records))
		for i, rec := range body.Records {
			preds[i] = rec["x"] * 2
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"predictions": preds})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPredictCallsTheVHostsModelWithItsSecretToken(t *testing.T) {
	var auth string
	srv := mlflowEcho(t, &auth)
	store := newMemStore(storage.MLModel{VHost: "a", Name: "double", Backend: inference.BackendMLflow, URL: srv.URL, TokenSecret: "ML_TOKEN"})
	svc := NewService(func() any { return store }, secretsOf{"a/ML_TOKEN": "s3cret"}, nil)

	got, err := svc.Predict(context.Background(), "a", "double", []inference.Row{{"x": 2.0}, {"x": 5.0}})
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if got[0]["prediction"] != 4.0 || got[1]["prediction"] != 10.0 {
		t.Errorf("predictions = %v", got)
	}
	if auth != "Bearer s3cret" {
		t.Errorf("Authorization = %q, want the vhost's secret", auth)
	}
}

func TestPredictForAModelAnotherVHostOwnsIsNotFound(t *testing.T) {
	store := newMemStore(storage.MLModel{VHost: "a", Name: "m", Backend: inference.BackendMLflow, URL: "http://x"})
	svc := NewService(func() any { return store }, nil, nil)
	_, err := svc.Predict(context.Background(), "b", "m", []inference.Row{{"x": 1.0}})
	if !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("err = %v, want ErrModelNotFound", err)
	}
}

func TestPredictRefusesAMissingTokenSecretInsteadOfCallingWithoutIt(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	t.Cleanup(srv.Close)
	store := newMemStore(storage.MLModel{VHost: "a", Name: "m", Backend: inference.BackendMLflow, URL: srv.URL, TokenSecret: "GONE"})
	svc := NewService(func() any { return store }, secretsOf{}, nil)
	_, err := svc.Predict(context.Background(), "a", "m", []inference.Row{{"x": 1.0}})
	if err == nil || !strings.Contains(err.Error(), "GONE") {
		t.Fatalf("err = %v, want a refusal naming the secret", err)
	}
	if called {
		t.Error("the model server was called without its token")
	}
}

func TestPredictRefusesTooManyRows(t *testing.T) {
	store := newMemStore(storage.MLModel{VHost: "a", Name: "m", Backend: inference.BackendMLflow, URL: "http://x"})
	svc := NewService(func() any { return store }, nil, nil)
	rows := make([]inference.Row, MaxRowsPerCall+1)
	if _, err := svc.Predict(context.Background(), "a", "m", rows); !errors.Is(err, ErrTooManyRows) {
		t.Fatalf("err = %v, want ErrTooManyRows", err)
	}
}

func TestPredictOnAStoreWithoutModels(t *testing.T) {
	svc := NewService(func() any { return nil }, nil, nil)
	if _, err := svc.Predict(context.Background(), "a", "m", []inference.Row{{}}); !errors.Is(err, storage.ErrMLModelsUnsupported) {
		t.Fatalf("err = %v, want ErrMLModelsUnsupported", err)
	}
}

func TestServingKeyAuthorizesOnlyItsOwnModel(t *testing.T) {
	store := newMemStore(
		storage.MLModel{VHost: "a", Name: "m", Backend: inference.BackendMLflow, URL: "http://x"},
		storage.MLModel{VHost: "a", Name: "other", Backend: inference.BackendMLflow, URL: "http://x"},
	)
	svc := NewService(func() any { return store }, nil, nil)
	ctx := context.Background()

	if err := svc.AuthorizeServing(ctx, "a", "m", "anything"); !errors.Is(err, ErrServingOff) {
		t.Fatalf("before a key exists: err = %v, want ErrServingOff", err)
	}

	key, err := svc.RotateServingKey(ctx, "a", "m")
	if err != nil {
		t.Fatalf("RotateServingKey: %v", err)
	}
	if len(key) < 32 || !strings.HasPrefix(key, "hml_") {
		t.Errorf("key %q is too short or unmarked", key)
	}
	if m, _ := store.GetMLModel(ctx, "a", "m"); m.ServingKeyHash == key || m.ServingKeyHash == "" {
		t.Error("the key was stored in clear, or not at all")
	}

	if err := svc.AuthorizeServing(ctx, "a", "m", key); err != nil {
		t.Errorf("the right key was refused: %v", err)
	}
	if err := svc.AuthorizeServing(ctx, "a", "m", key+"x"); !errors.Is(err, ErrBadServingKey) {
		t.Errorf("a wrong key: err = %v, want ErrBadServingKey", err)
	}
	if err := svc.AuthorizeServing(ctx, "a", "other", key); !errors.Is(err, ErrServingOff) {
		t.Errorf("m's key opened another model: err = %v", err)
	}
	if err := svc.AuthorizeServing(ctx, "b", "m", key); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("m's key opened another vhost: err = %v", err)
	}

	if err := svc.DisableServing(ctx, "a", "m"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AuthorizeServing(ctx, "a", "m", key); !errors.Is(err, ErrServingOff) {
		t.Errorf("after disabling: err = %v, want ErrServingOff", err)
	}
}
