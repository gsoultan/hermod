// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
