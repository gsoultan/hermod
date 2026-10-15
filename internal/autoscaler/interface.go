// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package autoscaler

import (
	"context"

	"github.com/gsoultan/hermod/internal/storage"
)

// WorkerManager defines the interface for managing workers (scaling, listing).
type WorkerManager interface {
	// ListWorkers returns the list of current workers and their count.
	ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error)
	// Scale updates the number of worker replicas.
	Scale(ctx context.Context, replicas int) error
}
