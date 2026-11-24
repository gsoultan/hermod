// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

//go:build linux

package hid

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// evIOCGrab is EVIOCGRAB: _IOW('E', 0x90, int) in the generic ioctl encoding,
// which every Linux architecture the agent is built for (amd64, arm64, arm,
// 386, riscv64) uses.
const evIOCGrab = 0x40044590

// grab takes the device for this process alone, so its key presses do not
// also reach the desktop.
func grab(f *os.File) error {
	if err := unix.IoctlSetInt(int(f.Fd()), evIOCGrab, 1); err != nil {
		return fmt.Errorf("grab %s (is another program reading it?): %w", f.Name(), err)
	}
	return nil
}

func supported() error { return nil }
