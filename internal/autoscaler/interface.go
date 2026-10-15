// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
