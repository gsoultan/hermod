// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package builtin

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"time"
)

// Blobs is where the engine keeps its datasets and model versions: named
// blobs per vhost, keyed by "/"-separated segments. Hermod keeps them in its
// metadata store, so every replica sees the same ones and they outlive a
// server's disk (internal/ml); DirBlobs keeps them in a directory, for a
// single server or a test.
//
// The engine writes most blobs once under a fresh key (a dataset part, a
// version's artifact), claims version numbers with Create, and replaces only
// small pointers with Put, so it needs no transactions.
type Blobs interface {
	// Put stores data under key, replacing what was there; a reader sees
	// the old blob or the new one.
	Put(ctx context.Context, vhost, key string, data []byte) error
	// Create stores data under key only if nothing is there yet, and
	// returns ErrBlobExists otherwise.
	Create(ctx context.Context, vhost, key string, data []byte) error
	// Get returns the blob under key, or ErrBlobNotFound.
	Get(ctx context.Context, vhost, key string) ([]byte, error)
	// List returns the blobs whose key starts with prefix (ending in "/"),
	// ordered by key, with their data when withData is set.
	List(ctx context.Context, vhost, prefix string, withData bool) ([]Blob, error)
	// Delete removes the blobs whose key starts with prefix (ending in "/")
	// and returns how many there were.
	Delete(ctx context.Context, vhost, prefix string) (int, error)
}

// Blob is one blob of a listing.
type Blob struct {
	Key       string
	Size      int64
	UpdatedAt time.Time
	Data      []byte
}

// The errors a Blobs answers with.
var (
	ErrBlobNotFound = errors.New("blob not found")
	ErrBlobExists   = errors.New("blob already exists")
	// ErrBlobTooLarge is a blob over what the store keeps in one: a dataset
	// sent in one piece too large, or a model too large to keep.
	ErrBlobTooLarge = errors.New("too large to keep")
)

// dirBlobs keeps blobs as files: DIR/{vhost}/{key}. It reaches them through
// an os.Root, so no key leads outside DIR, and writes each through a
// temporary file renamed into place.
type dirBlobs struct {
	root *os.Root
}

// DirBlobs keeps blobs in dir, which it creates. Close the root it holds with
// CloseBlobs when done.
func DirBlobs(dir string) (Blobs, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &dirBlobs{root: root}, nil
}

// CloseBlobs releases what a Blobs holds, if anything.
func CloseBlobs(b Blobs) error {
	if d, ok := b.(*dirBlobs); ok {
		return d.root.Close()
	}
	return nil
}

// file is the file of key in vhost; keys and vhosts are checked by the
// stores before they get here, and checked again so nothing escapes.
func (d *dirBlobs) file(vhost, key string) (string, error) {
	if err := checkName("vhost", vhost); err != nil {
		return "", err
	}
	if key == "" {
		return vhost, nil
	}
	for _, seg := range strings.Split(strings.TrimSuffix(key, "/"), "/") {
		if !validName.MatchString(seg) {
			return "", badRequest("blob key %q may hold only letters, digits, '_', '.' or '-' between '/'", key)
		}
	}
	return path.Join(vhost, key), nil
}

func (d *dirBlobs) Put(_ context.Context, vhost, key string, data []byte) error {
	name, err := d.file(vhost, key)
	if err != nil {
		return err
	}
	if err := d.root.MkdirAll(path.Dir(name), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(d.root, name, data)
}

func (d *dirBlobs) Create(_ context.Context, vhost, key string, data []byte) error {
	name, err := d.file(vhost, key)
	if err != nil {
		return err
	}
	if err := d.root.MkdirAll(path.Dir(name), 0o700); err != nil {
		return err
	}
	// Written aside first, then linked into place: a link to a taken name
	// fails, so only one of two creators wins, and the winner's blob is
	// whole when it appears.
	tmp := path.Join(path.Dir(name), tempName(".tmp-"))
	if err := d.root.WriteFile(tmp, data, 0o600); err != nil {
		_ = d.root.Remove(tmp)
		return err
	}
	defer func() { _ = d.root.Remove(tmp) }()
	if err := d.root.Link(tmp, name); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrBlobExists
		}
		return err
	}
	return nil
}

func (d *dirBlobs) Get(_ context.Context, vhost, key string) ([]byte, error) {
	name, err := d.file(vhost, key)
	if err != nil {
		return nil, err
	}
	raw, err := d.root.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrBlobNotFound
	}
	return raw, err
}

func (d *dirBlobs) List(_ context.Context, vhost, prefix string, withData bool) ([]Blob, error) {
	dir, err := d.file(vhost, prefix)
	if err != nil {
		return nil, err
	}
	var out []Blob
	err = fs.WalkDir(d.root.FS(), dir, func(p string, ent fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return fs.SkipAll
		}
		if err != nil {
			return err
		}
		if ent.IsDir() || strings.HasPrefix(ent.Name(), ".") {
			return nil
		}
		info, err := ent.Info()
		if err != nil {
			return err
		}
		b := Blob{Key: strings.TrimPrefix(p, vhost+"/"), Size: info.Size(), UpdatedAt: info.ModTime().UTC()}
		if withData {
			if b.Data, err = d.root.ReadFile(p); err != nil {
				return err
			}
		}
		out = append(out, b)
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b Blob) int { return strings.Compare(a.Key, b.Key) })
	return out, nil
}

func (d *dirBlobs) Delete(ctx context.Context, vhost, prefix string) (int, error) {
	list, err := d.List(ctx, vhost, prefix, false)
	if err != nil {
		return 0, err
	}
	dir, _ := d.file(vhost, prefix)
	if len(list) > 0 || exists(d.root, dir) {
		if err := removeDir(d.root, dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return 0, err
		}
	}
	return len(list), nil
}
