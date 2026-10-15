// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
