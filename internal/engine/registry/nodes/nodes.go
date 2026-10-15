// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

// Package nodes is the one import that makes every workflow node type
// available.
//
// Each node registers itself from an init() that calls
// interfaces.RegisterNodeExecutor, so a node type exists only if its package is
// linked into the binary. Listing them here means a caller blank-imports this
// one package -- cmd/hermod/main.go does -- instead of tracking five, and a new
// node category is wired up by adding a line here rather than by remembering to
// edit main.
package nodes

import (
	// Imported for side effects only: each init() registers that category's
	// node executors. Removing a line silently drops those node types from the
	// binary, and a workflow using one then fails at runtime rather than at
	// build time.
	_ "github.com/gsoultan/hermod/internal/engine/registry/nodes/ai"
	_ "github.com/gsoultan/hermod/internal/engine/registry/nodes/ai/agent"
	_ "github.com/gsoultan/hermod/internal/engine/registry/nodes/control"
	_ "github.com/gsoultan/hermod/internal/engine/registry/nodes/core"
	_ "github.com/gsoultan/hermod/internal/engine/registry/nodes/flow"
	_ "github.com/gsoultan/hermod/internal/engine/registry/nodes/reliability"
	_ "github.com/gsoultan/hermod/internal/engine/registry/nodes/util"
)
