// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package registry

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/factory"
)

func TestReproTestSinkPostgres(t *testing.T) {
	r := NewRegistry(nil)
	cfg := factory.SinkConfig{
		Type: "postgres",
		Config: map[string]string{
			"host":     "localhost",
			"port":     "5432",
			"user":     "postgres",
			"password": "postgres",
			"dbname":   "postgres",
			"table":    "t",
		},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	start := time.Now()
	err := r.TestSink(ctx, cfg)
	t.Logf("elapsed=%v err=%v", time.Since(start), err)
}
