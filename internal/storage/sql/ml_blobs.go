// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package sql

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// mlBlobID is a chunk's row key: vhost + "/" + key + "#" + chunk. A vhost
// name holds no "/", and "#" never ends a key's last segment's chunk number,
// so two chunks cannot share one.
func mlBlobID(vhost, key string, chunk int) string {
	return vhost + "/" + key + "#" + strconv.Itoa(chunk)
}

// likePrefix is the LIKE pattern for keys starting with prefix, its own
// wildcards escaped (see likeContains).
func likePrefix(prefix string) string {
	r := strings.NewReplacer(likeEscape, likeEscape+likeEscape, "%", likeEscape+"%", "_", likeEscape+"_")
	return r.Replace(prefix) + "%"
}

// putMLBlobTx replaces key's chunks inside tx.
func (s *sqlStorage) putMLBlobTx(ctx context.Context, tx *sql.Tx, vhost, key string, data []byte) error {
	if _, err := tx.ExecContext(ctx, s.prepareQuery(s.queries.get(QueryDeleteMLBlob)), vhost, key); err != nil {
		return err
	}
	chunks := storage.MLBlobChunks(data)
	now := time.Now().UTC()
	insert := s.prepareQuery(s.queries.get(QueryInsertMLBlobChunk))
	for i, c := range chunks {
		if _, err := tx.ExecContext(ctx, insert, mlBlobID(vhost, key, i), vhost, key, i, len(chunks), int64(len(data)), c, now); err != nil {
			return err
		}
	}
	return nil
}

// writeMLBlob runs one transaction that, unless create finds the key taken,
// writes data under key. Two servers replacing the same key at once collide
// on a chunk's primary key; the loser tries again.
func (s *sqlStorage) writeMLBlob(ctx context.Context, vhost, key string, data []byte, create bool) error {
	if err := storage.ValidateMLBlob(vhost, key, len(data)); err != nil {
		return err
	}
	var lastErr error
	for range 3 {
		lastErr = s.writeMLBlobOnce(ctx, vhost, key, data, create)
		if lastErr == nil || errors.Is(lastErr, storage.ErrMLBlobExists) {
			return lastErr
		}
		if create && s.mlBlobExists(ctx, vhost, key) {
			return storage.ErrMLBlobExists
		}
	}
	return fmt.Errorf("saving ML blob %q of vhost %q: %w", key, vhost, lastErr)
}

func (s *sqlStorage) writeMLBlobOnce(ctx context.Context, vhost, key string, data []byte, create bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if create {
		var n int
		if err := tx.QueryRowContext(ctx, s.prepareQuery(s.queries.get(QueryCountMLBlob)), vhost, key).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return storage.ErrMLBlobExists
		}
	}
	if err := s.putMLBlobTx(ctx, tx, vhost, key, data); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqlStorage) mlBlobExists(ctx context.Context, vhost, key string) bool {
	var n int
	err := s.queryRow(ctx, s.queries.get(QueryCountMLBlob), vhost, key).Scan(&n)
	return err == nil && n > 0
}

func (s *sqlStorage) PutMLBlob(ctx context.Context, vhost, key string, data []byte) error {
	return s.writeMLBlob(ctx, vhost, key, data, false)
}

func (s *sqlStorage) CreateMLBlob(ctx context.Context, vhost, key string, data []byte) error {
	return s.writeMLBlob(ctx, vhost, key, data, true)
}

func (s *sqlStorage) GetMLBlob(ctx context.Context, vhost, key string) ([]byte, error) {
	if err := storage.ValidateMLBlob(vhost, key, 0); err != nil {
		return nil, err
	}
	rows, err := s.query(ctx, s.queries.get(QueryGetMLBlob), vhost, key)
	if err != nil {
		return nil, fmt.Errorf("reading ML blob %q of vhost %q: %w", key, vhost, err)
	}
	defer func() { _ = rows.Close() }()
	var parts [][]byte
	want := 0
	for rows.Next() {
		var chunk, chunks int
		var data []byte
		if err := rows.Scan(&chunk, &chunks, &data); err != nil {
			return nil, fmt.Errorf("reading ML blob %q of vhost %q: %w", key, vhost, err)
		}
		if chunk != len(parts) {
			return nil, fmt.Errorf("ML blob %q of vhost %q is missing chunk %d", key, vhost, len(parts))
		}
		parts, want = append(parts, data), chunks
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading ML blob %q of vhost %q: %w", key, vhost, err)
	}
	if len(parts) == 0 {
		return nil, storage.ErrNotFound
	}
	if len(parts) != want {
		return nil, fmt.Errorf("ML blob %q of vhost %q has %d of its %d chunks", key, vhost, len(parts), want)
	}
	return bytes.Join(parts, nil), nil
}

func (s *sqlStorage) ListMLBlobs(ctx context.Context, vhost, prefix string, withData bool) ([]storage.MLBlob, error) {
	if err := storage.ValidateMLBlobPrefix(vhost, prefix); err != nil {
		return nil, err
	}
	var out []storage.MLBlob
	var err error
	if withData {
		out, err = s.listMLBlobsWithData(ctx, vhost, prefix)
	} else {
		out, err = s.listMLBlobs(ctx, vhost, prefix)
	}
	if err != nil {
		return nil, fmt.Errorf("listing ML blobs %q of vhost %q: %w", prefix, vhost, err)
	}
	// By key as bytes, whatever the database's collation.
	slices.SortFunc(out, func(a, b storage.MLBlob) int { return strings.Compare(a.Key, b.Key) })
	return out, nil
}

func (s *sqlStorage) listMLBlobs(ctx context.Context, vhost, prefix string) ([]storage.MLBlob, error) {
	rows, err := s.query(ctx, s.queries.get(QueryListMLBlobs), vhost, likePrefix(prefix))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []storage.MLBlob
	for rows.Next() {
		var b storage.MLBlob
		if err := rows.Scan(&b.Key, &b.Size, &b.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *sqlStorage) listMLBlobsWithData(ctx context.Context, vhost, prefix string) ([]storage.MLBlob, error) {
	rows, err := s.query(ctx, s.queries.get(QueryListMLBlobsData), vhost, likePrefix(prefix))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []storage.MLBlob
	var parts [][]byte
	want := 0
	flush := func() error {
		if len(out) == 0 {
			return nil
		}
		if len(parts) != want {
			return fmt.Errorf("ML blob %q has %d of its %d chunks", out[len(out)-1].Key, len(parts), want)
		}
		out[len(out)-1].Data = bytes.Join(parts, nil)
		return nil
	}
	for rows.Next() {
		var b storage.MLBlob
		var chunk, chunks int
		var data []byte
		if err := rows.Scan(&b.Key, &chunk, &chunks, &b.Size, &b.UpdatedAt, &data); err != nil {
			return nil, err
		}
		if chunk == 0 {
			if err := flush(); err != nil {
				return nil, err
			}
			out, parts, want = append(out, b), nil, chunks
		}
		parts = append(parts, data)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, flush()
}

func (s *sqlStorage) DeleteMLBlobs(ctx context.Context, vhost, prefix string) (int, error) {
	if err := storage.ValidateMLBlobPrefix(vhost, prefix); err != nil {
		return 0, err
	}
	var n int
	if err := s.queryRow(ctx, s.queries.get(QueryCountMLBlobs), vhost, likePrefix(prefix)).Scan(&n); err != nil {
		return 0, fmt.Errorf("deleting ML blobs %q of vhost %q: %w", prefix, vhost, err)
	}
	if _, err := s.exec(ctx, s.queries.get(QueryDeleteMLBlobs), vhost, likePrefix(prefix)); err != nil {
		return 0, fmt.Errorf("deleting ML blobs %q of vhost %q: %w", prefix, vhost, err)
	}
	return n, nil
}
