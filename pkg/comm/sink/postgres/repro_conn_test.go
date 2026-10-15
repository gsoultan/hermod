// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package postgres

import (
	"context"
	"testing"
	"time"
)

func TestReproPingTimeout(t *testing.T) {
	dsns := []string{
		"postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable",
		// Unreachable: non-routable IP that silently drops packets (firewall-like).
		"postgres://postgres:postgres@10.255.255.1:5432/postgres?sslmode=disable",
		// Closed port on localhost (connection refused, should fail fast).
		"postgres://postgres:postgres@localhost:1/postgres?sslmode=disable",
	}
	for _, dsn := range dsns {
		t.Run(dsn, func(t *testing.T) {
			s := NewPostgresSink(dsn, "t", nil, false, "", "", "", "auto", false, false)
			defer s.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			start := time.Now()
			err := s.Ping(ctx)
			t.Logf("dsn=%s elapsed=%v err=%v", dsn, time.Since(start), err)
		})
	}
}
