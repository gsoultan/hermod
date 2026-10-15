// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	// modernc.org/sqlite registers the pure-Go "sqlite" database/sql driver via init().
	"github.com/gsoultan/hermod"
	_ "modernc.org/sqlite"
)

type SQLiteStateStore struct {
	db *sql.DB
}

func NewSQLiteStateStore(path string) (hermod.StateStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite state store: %w", err)
	}
	db.SetMaxOpenConns(1)

	_, err = db.Exec(commonQueries[QueryInitTable])
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create states table: %w", err)
	}

	return &SQLiteStateStore{db: db}, nil
}

func (s *SQLiteStateStore) Get(ctx context.Context, key string) ([]byte, error) {
	var val []byte
	err := s.db.QueryRowContext(ctx, commonQueries[QueryGet], key).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return val, nil
}

func (s *SQLiteStateStore) Set(ctx context.Context, key string, value []byte) error {
	_, err := s.db.ExecContext(ctx, commonQueries[QuerySet], key, value)
	return err
}

func (s *SQLiteStateStore) Delete(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, commonQueries[QueryDelete], key)
	return err
}

func (s *SQLiteStateStore) Close() error {
	return s.db.Close()
}
