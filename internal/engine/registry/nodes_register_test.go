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

// This file lives in the external test package (registry_test) so it can
// blank-import the node-executor packages without creating an import cycle
// (the executor packages import the registry package). Importing the aggregator
// runs every executor's init(), registering the "router", "condition",
// "transformation", "switch", etc. executors into the shared registry. Without
// this, the internal registry tests that exercise RunWorkflowNode would route
// through the empty-executor fallback and observe empty branches / untransformed
// messages.
import (
	_ "github.com/gsoultan/hermod/internal/engine/registry/nodes"
)
