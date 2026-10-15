// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
