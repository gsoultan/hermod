// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MLBlob is one named blob of Hermod's built-in ML engine: a dataset part, a
// model version's meta or its artifact (pkg/ml/builtin). Keeping them in the
// metadata store, rather than on one server's disk, is what lets every Hermod
// replica train on a dataset another one filled and score a model another one
// trained, and keeps them across a restart onto a fresh disk.
//
// A blob belongs to one vhost and is named by a key of path-like segments
// ("models/churn/b3/meta"). Data is empty in a listing made without it.
type MLBlob struct {
	Key       string    `json:"key"`
	Size      int64     `json:"size"`
	UpdatedAt time.Time `json:"updated_at"`
	Data      []byte    `json:"-"`
}

// Limits on a blob. A blob is stored in chunks of MLBlobChunkBytes, which
// keeps every row or document well under what each backend takes in one
// (MongoDB's is 16 MB) and keeps a write from holding the whole of a large
// blob in one statement.
const (
	MaxMLBlobBytes   = 256 << 20
	MaxMLBlobKeyLen  = 512
	MLBlobChunkBytes = 4 << 20
)

// ErrMLBlobExists is CreateMLBlob's answer when the key is taken.
var ErrMLBlobExists = errors.New("the ML blob already exists")

// ValidateMLBlob reports what is wrong with a blob's vhost, key or size
// before it is stored. A key is non-empty segments joined by "/", none of
// "." or "..", with no control characters.
func ValidateMLBlob(vhost, key string, size int) error {
	if vhost == "" {
		return errors.New("an ML blob belongs to one vhost: name it")
	}
	if err := validMLBlobPath(key, false); err != nil {
		return err
	}
	if size > MaxMLBlobBytes {
		return fmt.Errorf("ML blob %q is %d bytes, over the %d MB limit", key, size, MaxMLBlobBytes>>20)
	}
	return nil
}

// ValidateMLBlobPrefix reports what is wrong with a prefix to list or delete
// by: "" (every blob of the vhost), or a key's leading segments ending in "/".
func ValidateMLBlobPrefix(vhost, prefix string) error {
	if vhost == "" {
		return errors.New("an ML blob belongs to one vhost: name it")
	}
	if prefix == "" {
		return nil
	}
	if !strings.HasSuffix(prefix, "/") {
		return fmt.Errorf("ML blob prefix %q must end in '/'", prefix)
	}
	return validMLBlobPath(strings.TrimSuffix(prefix, "/"), true)
}

func validMLBlobPath(key string, prefix bool) error {
	what := "key"
	if prefix {
		what = "prefix"
	}
	if key == "" || len(key) > MaxMLBlobKeyLen {
		return fmt.Errorf("ML blob %s must be 1 to %d bytes", what, MaxMLBlobKeyLen)
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("ML blob %s %q has an empty, '.' or '..' segment", what, key)
		}
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("ML blob %s %q holds a control character", what, key)
		}
	}
	return nil
}

// MLBlobChunks splits data into the chunks it is stored as: at least one,
// so an empty blob still has a row.
func MLBlobChunks(data []byte) [][]byte {
	if len(data) == 0 {
		return [][]byte{{}}
	}
	var out [][]byte
	for len(data) > 0 {
		n := min(len(data), MLBlobChunkBytes)
		out = append(out, data[:n])
		data = data[n:]
	}
	return out
}

// MLBlobStore is implemented by a metadata store that keeps the built-in ML
// engine's blobs. Like MLModelStore it is separate from Storage; callers find
// it with a type assertion.
type MLBlobStore interface {
	// PutMLBlob stores data under key, replacing any blob there. A reader
	// sees the old blob or the new one, never a mix.
	PutMLBlob(ctx context.Context, vhost, key string, data []byte) error
	// CreateMLBlob stores data under key only if nothing is there yet, and
	// returns ErrMLBlobExists otherwise: two servers claiming the same key
	// cannot both succeed.
	CreateMLBlob(ctx context.Context, vhost, key string, data []byte) error
	// GetMLBlob returns the blob under key, or ErrNotFound.
	GetMLBlob(ctx context.Context, vhost, key string) ([]byte, error)
	// ListMLBlobs returns the blobs whose key starts with prefix ("" for
	// all), ordered by key, with their data only when withData is set.
	ListMLBlobs(ctx context.Context, vhost, prefix string, withData bool) ([]MLBlob, error)
	// DeleteMLBlobs removes the blobs whose key starts with prefix ("" for
	// every blob of the vhost) and returns how many there were.
	DeleteMLBlobs(ctx context.Context, vhost, prefix string) (int, error)
}
