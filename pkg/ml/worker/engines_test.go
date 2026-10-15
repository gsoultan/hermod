// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package worker

import "testing"

func TestBuiltinVersionsNeverLookLikeTheWorkers(t *testing.T) {
	cases := []struct {
		version string
		n       int
		builtin bool
	}{
		{"b1", 1, true},
		{"b12", 12, true},
		{"1", 0, false},  // the worker's
		{"12", 0, false}, // the worker's
		{"b0", 0, false},
		{"b01", 0, false},
		{"b-3", 0, false},
		{"b", 0, false},
		{"", 0, false},
		{"bx", 0, false},
	}
	for _, tc := range cases {
		n, ok := ParseBuiltinVersion(tc.version)
		if ok != tc.builtin || n != tc.n || IsBuiltinVersion(tc.version) != tc.builtin {
			t.Errorf("ParseBuiltinVersion(%q) = %d, %v; want %d, %v", tc.version, n, ok, tc.n, tc.builtin)
		}
	}
	if got := BuiltinVersion(7); got != "b7" {
		t.Errorf("BuiltinVersion(7) = %q", got)
	}
	if EngineOf("b3") != EngineBuiltin || EngineOf("3") != EngineWorker {
		t.Error("EngineOf does not tell the engines apart")
	}
}
