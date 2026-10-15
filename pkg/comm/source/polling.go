// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package source

import (
	"context"
	"database/sql"
	"time"

	"github.com/gsoultan/hermod"
)

// PollingSource is a generic implementation of a source that polls a database for changes.
type PollingSource struct {
	db             *sql.DB
	query          string
	interval       time.Duration
	watermarkCol   string
	watermarkValue any
	logger         hermod.Logger
}

func NewPollingSource(db *sql.DB, query string, interval time.Duration, watermarkCol string, initialWatermark any) *PollingSource {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &PollingSource{
		db:             db,
		query:          query,
		interval:       interval,
		watermarkCol:   watermarkCol,
		watermarkValue: initialWatermark,
	}
}

func (s *PollingSource) SetLogger(logger hermod.Logger) {
	s.logger = logger
}

func (s *PollingSource) Read(ctx context.Context) (hermod.Message, error) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			msg, err := s.poll(ctx)
			if err != nil {
				if s.logger != nil {
					s.logger.Error("Polling error", "error", err)
				}
				return nil, err
			}
			if msg != nil {
				return msg, nil
			}
		}
	}
}

func (s *PollingSource) poll(ctx context.Context) (hermod.Message, error) {
	// Implementation will vary based on specific database and query.
	// This is a template. Specific implementations like MySQLPollingSource will extend this.
	return nil, nil
}

func (s *PollingSource) Ack(ctx context.Context, msg hermod.Message) error {
	if s.watermarkCol != "" && msg != nil {
		if val, ok := msg.Data()[s.watermarkCol]; ok {
			s.watermarkValue = val
		}
	}
	return nil
}

func (s *PollingSource) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *PollingSource) Close() error {
	return s.db.Close()
}
