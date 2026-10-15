// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
