// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
