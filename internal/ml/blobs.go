package ml

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/builtin"
)

// ErrNoBlobStore is the built-in engine's answer while Hermod's storage
// cannot keep its datasets and versions: it is not set up yet, or its
// backend keeps no ML blobs and no directory was given instead.
var ErrNoBlobStore = errors.New("the metadata storage cannot keep the built-in ML engine's datasets and models " +
	"(it is not set up yet, or its backend keeps none)")

// BuiltinBlobs keeps the built-in engine's datasets and model versions in
// Hermod's metadata store, the one store returns at each call (setup and a
// database switch replace it while Hermod runs). So every replica sharing
// the database serves every model, and none depends on a server's disk.
// fallback, which may be nil, is used only by a store whose backend keeps no
// ML blobs.
func BuiltinBlobs(store func() any, fallback builtin.Blobs) builtin.Blobs {
	return &storeBlobs{store: store, fallback: fallback}
}

type storeBlobs struct {
	store    func() any
	fallback builtin.Blobs
	warn     sync.Once
}

// target is the store in force as blobs, or the fallback.
func (b *storeBlobs) target() (builtin.Blobs, error) {
	st := b.store()
	if st == nil || (reflect.ValueOf(st).Kind() == reflect.Pointer && reflect.ValueOf(st).IsNil()) {
		return nil, ErrNoBlobStore
	}
	if bs, ok := st.(storage.MLBlobStore); ok {
		return blobStore{bs}, nil
	}
	if b.fallback == nil {
		return nil, ErrNoBlobStore
	}
	b.warn.Do(func() {
		slog.Warn("ml: Hermod's storage backend keeps no ML blobs, so the built-in engine keeps its datasets and " +
			"models in HERMOD_ML_BUILTIN_DIR; with more than one replica, that directory must be shared")
	})
	return b.fallback, nil
}

func (b *storeBlobs) Put(ctx context.Context, vhost, key string, data []byte) error {
	t, err := b.target()
	if err != nil {
		return err
	}
	return t.Put(ctx, vhost, key, data)
}

func (b *storeBlobs) Create(ctx context.Context, vhost, key string, data []byte) error {
	t, err := b.target()
	if err != nil {
		return err
	}
	return t.Create(ctx, vhost, key, data)
}

func (b *storeBlobs) Get(ctx context.Context, vhost, key string) ([]byte, error) {
	t, err := b.target()
	if err != nil {
		return nil, err
	}
	return t.Get(ctx, vhost, key)
}

func (b *storeBlobs) List(ctx context.Context, vhost, prefix string, withData bool) ([]builtin.Blob, error) {
	t, err := b.target()
	if err != nil {
		return nil, err
	}
	return t.List(ctx, vhost, prefix, withData)
}

func (b *storeBlobs) Delete(ctx context.Context, vhost, prefix string) (int, error) {
	t, err := b.target()
	if err != nil {
		return 0, err
	}
	return t.Delete(ctx, vhost, prefix)
}

// blobStore is a storage.MLBlobStore as the engine's Blobs, with the
// engine's errors.
type blobStore struct{ s storage.MLBlobStore }

func tooLarge(key string, size int) error {
	if size > storage.MaxMLBlobBytes {
		return fmt.Errorf("%w: %q is %d MB, and Hermod's storage keeps at most %d MB in one",
			builtin.ErrBlobTooLarge, key, size>>20, storage.MaxMLBlobBytes>>20)
	}
	return nil
}

func (b blobStore) Put(ctx context.Context, vhost, key string, data []byte) error {
	if err := tooLarge(key, len(data)); err != nil {
		return err
	}
	return b.s.PutMLBlob(ctx, vhost, key, data)
}

func (b blobStore) Create(ctx context.Context, vhost, key string, data []byte) error {
	if err := tooLarge(key, len(data)); err != nil {
		return err
	}
	err := b.s.CreateMLBlob(ctx, vhost, key, data)
	if errors.Is(err, storage.ErrMLBlobExists) {
		return fmt.Errorf("%w: %s", builtin.ErrBlobExists, key)
	}
	return err
}

func (b blobStore) Get(ctx context.Context, vhost, key string) ([]byte, error) {
	data, err := b.s.GetMLBlob(ctx, vhost, key)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, fmt.Errorf("%w: %s", builtin.ErrBlobNotFound, key)
	}
	return data, err
}

func (b blobStore) List(ctx context.Context, vhost, prefix string, withData bool) ([]builtin.Blob, error) {
	list, err := b.s.ListMLBlobs(ctx, vhost, prefix, withData)
	if err != nil {
		return nil, err
	}
	out := make([]builtin.Blob, len(list))
	for i, m := range list {
		out[i] = builtin.Blob{Key: m.Key, Size: m.Size, UpdatedAt: m.UpdatedAt, Data: m.Data}
	}
	return out, nil
}

func (b blobStore) Delete(ctx context.Context, vhost, prefix string) (int, error) {
	return b.s.DeleteMLBlobs(ctx, vhost, prefix)
}
