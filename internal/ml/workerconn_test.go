package ml

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// ---------------------------------------------------------------------------
// The ML worker connected from the console: kept in Hermod's settings with
// its token encrypted, in force the moment it is saved, and never in force
// over a worker the environment names.
// ---------------------------------------------------------------------------

// settingsStore is the settings part of a storage backend, in memory.
type settingsStore struct {
	storage.Storage
	mu       sync.Mutex
	settings map[string]string
	fail     error
}

func newSettingsStore() *settingsStore { return &settingsStore{settings: map[string]string{}} }

func (s *settingsStore) GetSetting(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return "", s.fail
	}
	return s.settings[key], nil
}

func (s *settingsStore) SaveSetting(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return s.fail
	}
	s.settings[key] = value
	return nil
}

func noEnv() *worker.Provider { return worker.NewProvider(func() *worker.Client { return nil }) }

func ptr(s string) *string { return &s }

func TestSaveWorkerConnectionEncryptsTheTokenAndReadsItBack(t *testing.T) {
	ctx := t.Context()
	store := newSettingsStore()
	p := noEnv()

	if _, err := SaveWorkerConnection(ctx, store, p, "http://hermod-ml:8090/", ptr("s3cret-token")); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw := store.settings[storage.MLWorkerSettingKey]
	if strings.Contains(raw, "s3cret-token") || !strings.Contains(raw, "enc:") {
		t.Fatalf("stored %s: the token must be encrypted", raw)
	}
	got, err := LoadWorkerConnection(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "http://hermod-ml:8090" || got.Token != "s3cret-token" {
		t.Errorf("loaded %+v", got)
	}
}

func TestSavedWorkerIsInForceAtOnce(t *testing.T) {
	ctx := t.Context()
	store := newSettingsStore()
	p := noEnv()
	if _, err := SaveWorkerConnection(ctx, store, p, "http://hermod-ml:8090", ptr("tok")); err != nil {
		t.Fatal(err)
	}
	c, src := p.Current()
	if c == nil || c.URL != "http://hermod-ml:8090" || c.Token != "tok" || src != worker.SourceSaved {
		t.Fatalf("Current() = %+v, %q", c, src)
	}

	// A service built afterwards trains on it.
	svc := NewService(func() any { return store }, nil, nil).WithWorkerProvider(p)
	w, err := svc.Worker()
	if err != nil || w.URL != "http://hermod-ml:8090" {
		t.Fatalf("Worker() = %+v, %v", w, err)
	}
}

func TestSaveWorkerConnectionKeepsTheTokenWhenNoneIsGiven(t *testing.T) {
	ctx := t.Context()
	store := newSettingsStore()
	p := noEnv()
	if _, err := SaveWorkerConnection(ctx, store, p, "http://a:8090", ptr("tok")); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveWorkerConnection(ctx, store, p, "http://b:8090", nil); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadWorkerConnection(ctx, store)
	if got.URL != "http://b:8090" || got.Token != "tok" {
		t.Errorf("after changing only the address: %+v", got)
	}

	// An empty token is a choice: a worker without one.
	if _, err := SaveWorkerConnection(ctx, store, p, "http://b:8090", ptr("")); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadWorkerConnection(ctx, store); got.Token != "" {
		t.Errorf("token %q, want it cleared", got.Token)
	}
}

func TestSaveWorkerConnectionWithNoAddressDisconnects(t *testing.T) {
	ctx := t.Context()
	store := newSettingsStore()
	p := noEnv()
	_, _ = SaveWorkerConnection(ctx, store, p, "http://a:8090", ptr("tok"))
	if _, err := SaveWorkerConnection(ctx, store, p, "", nil); err != nil {
		t.Fatal(err)
	}
	if c, src := p.Current(); c != nil || src != worker.SourceNone {
		t.Errorf("still connected: %+v %q", c, src)
	}
	if got, _ := LoadWorkerConnection(ctx, store); got != (WorkerConnection{}) {
		t.Errorf("still saved: %+v", got)
	}
}

func TestSaveWorkerConnectionRefuses(t *testing.T) {
	ctx := t.Context()
	env := worker.NewProvider(func() *worker.Client { return worker.New("http://env:8090", "e", nil) })
	if _, err := SaveWorkerConnection(ctx, newSettingsStore(), env, "http://a:8090", ptr("t")); !errors.Is(err, ErrWorkerFromEnvironment) {
		t.Errorf("with the environment's worker: %v, want ErrWorkerFromEnvironment", err)
	}
	if _, err := SaveWorkerConnection(ctx, newSettingsStore(), noEnv(), "hermod-ml:8090", nil); !errors.Is(err, ErrBadWorkerURL) {
		t.Errorf("a bad address: %v, want ErrBadWorkerURL", err)
	}
	if _, err := SaveWorkerConnection(ctx, struct{}{}, noEnv(), "http://a:8090", nil); !errors.Is(err, ErrSettingsUnsupported) {
		t.Errorf("a store without settings: %v, want ErrSettingsUnsupported", err)
	}
	broken := newSettingsStore()
	broken.fail = errors.New("disk full")
	p := noEnv()
	if _, err := SaveWorkerConnection(ctx, broken, p, "http://a:8090", ptr("t")); err == nil {
		t.Error("a failed write was reported as saved")
	}
	if c, _ := p.Current(); c != nil {
		t.Errorf("a worker that was not saved is in force: %+v", c)
	}
}

// Another Hermod process sharing the database (a worker started with
// --mode=worker on the same store) picks the saved worker up on its next
// read.
func TestRefreshWorkerReadsTheSavedWorker(t *testing.T) {
	ctx := t.Context()
	store := newSettingsStore()
	if _, err := SaveWorkerConnection(ctx, store, noEnv(), "http://a:8090", ptr("tok")); err != nil {
		t.Fatal(err)
	}
	other := noEnv()
	RefreshWorker(ctx, store, other)
	if c, src := other.Current(); c == nil || c.URL != "http://a:8090" || c.Token != "tok" || src != worker.SourceSaved {
		t.Fatalf("Current() = %+v, %q", c, src)
	}

	// A store that holds no settings changes nothing.
	RefreshWorker(ctx, struct{}{}, noEnv())
	RefreshWorker(ctx, nil, noEnv())
}
