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
