// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package optimizer

import (
	"context"

	"github.com/gsoultan/hermod/pkg/engine"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// AIOptimizer defines the interface for AI-driven performance optimization.
type AIOptimizer interface {
	// Optimize suggests and applies optimizations for a running engine.
	Optimize(ctx context.Context, id string, e *engine.Engine, status telemetry.StatusUpdate) error
}
