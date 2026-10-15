// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
