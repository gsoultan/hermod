// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package worker

import (
	"strconv"
	"strings"
)

// The engines that train Hermod's models: its built-in one, in Hermod's own
// process, and the hermod-ml worker. A version says which (Version.Engine),
// and so does its id: the worker numbers its versions 1, 2, …, and the
// built-in engine b1, b2, …, so two versions of one model never share an id
// whichever engine trained them, and a model's live version alone says which
// engine scores it.
const (
	EngineBuiltin = "builtin"
	EngineWorker  = "worker"

	// BuiltinVersionPrefix starts every version the built-in engine trains.
	BuiltinVersionPrefix = "b"
)

// BuiltinVersion is the built-in engine's version id for number n.
func BuiltinVersion(n int) string { return BuiltinVersionPrefix + strconv.Itoa(n) }

// ParseBuiltinVersion is the number of a built-in version id ("b3" is 3), and
// false for anything else, the worker's ids included.
func ParseBuiltinVersion(v string) (int, bool) {
	digits, ok := strings.CutPrefix(v, BuiltinVersionPrefix)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n <= 0 || strconv.Itoa(n) != digits {
		return 0, false
	}
	return n, true
}

// IsBuiltinVersion reports whether the built-in engine trained version v.
func IsBuiltinVersion(v string) bool {
	_, ok := ParseBuiltinVersion(v)
	return ok
}

// EngineOf is the engine that holds version v.
func EngineOf(v string) string {
	if IsBuiltinVersion(v) {
		return EngineBuiltin
	}
	return EngineWorker
}
