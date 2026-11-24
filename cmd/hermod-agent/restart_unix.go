// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

//go:build !windows

package main

import (
	"os"
	"syscall"
)

// restartSelf replaces this process with the updated binary at exe, with the
// same arguments and environment. The process id stays the same, so systemd,
// launchd and rc scripts keep tracking it.
func restartSelf(exe string, _ bool) error {
	return syscall.Exec(exe, append([]string{exe}, os.Args[1:]...), os.Environ()) //nolint:gosec // G204: this program's own binary, just verified
}
