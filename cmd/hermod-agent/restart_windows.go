// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

//go:build windows

package main

import (
	"os"
	"os/exec"
)

// restartExitCode ends a Windows service so the Service Control Manager's
// recovery action (restart after 10 s, set at install) starts the new binary.
const restartExitCode = 3

// restartSelf starts the updated binary. A service exits with a failure code
// and is restarted by the Service Control Manager; run from a console, the
// new binary is started with the same arguments and this one exits.
func restartSelf(exe string, interactive bool) error {
	if !interactive {
		os.Exit(restartExitCode)
	}
	cmd := exec.Command(exe, os.Args[1:]...) //nolint:gosec // G204: this program's own binary, just verified
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
