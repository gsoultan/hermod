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
	"fmt"
	"os"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

// TestMain enforces the message ownership contract across every test in this
// package.
//
// Messages are pooled and reference-counted by hand: roughly three dozen
// Retain/Release sites spread over the traversal, the node executors and the
// writer. Releasing one time too many hands the message back to the pool while
// an owner still holds it; the pool then refills it for a different message and
// the stale owner delivers the wrong payload. The observable symptom is not an
// error — it is some messages delivered twice, others never delivered, and the
// totals still adding up. That is how a data-loss bug lived in
// traversal.runNode's source branch without a single failing test.
//
// message.OverReleaseCount is the tripwire: it counts every Release that
// arrives after a refcount already reached zero. Any non-zero value is a
// correctness bug somewhere in this package's call graph, so the whole package
// fails rather than leaving it to be discovered in production.
func TestMain(m *testing.M) {
	message.ResetOverReleaseCount()

	code := m.Run()

	if n := message.OverReleaseCount(); n != 0 && code == 0 {
		fmt.Fprintf(os.Stderr,
			"\nFAIL: %d message over-release(s) during this package's tests.\n"+
				"A message was released after its refcount reached zero, so it went back to the\n"+
				"pool while still referenced. Expect duplicated and lost messages, not an error.\n"+
				"Check every Retain/Release pair on the paths the failing tests exercised.\n", n)
		code = 1
	}

	os.Exit(code)
}
