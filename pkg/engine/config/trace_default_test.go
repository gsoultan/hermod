// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package config

import "testing"

// Tracing writes the message payload into message_trace_steps, a row per node
// per message — the largest table Hermod owns and the one that filled a
// production PostgreSQL by 50 GB in a couple of hours. A default of 1.0 means
// any engine reaching this struct without the per-workflow override traces
// every message. The registry does apply the override, so the default only
// governs paths that forgot to, and those are exactly the ones nobody is
// watching.
//
// A default that spends unbounded disk when a caller forgets a line is the
// wrong way round. Off is recoverable by configuration; a full disk is not.
func TestDefaultConfig_DoesNotTraceEveryMessage(t *testing.T) {
	if got := DefaultConfig().TraceSampleRate; got != 0 {
		t.Errorf("DefaultConfig().TraceSampleRate = %v, want 0; any path that does not "+
			"set the per-workflow rate would otherwise write the full payload for "+
			"every message that passes through it", got)
	}
}
