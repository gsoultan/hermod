// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

//go:build !(linux || windows || (darwin && cgo))

package detect

import "go.bug.st/serial"

// SystemPorts lists serial ports by name only. USB details need the
// platform's enumerator, which on macOS requires cgo.
func SystemPorts() ([]Port, error) {
	names, err := serial.GetPortsList()
	if err != nil {
		return nil, err
	}
	out := make([]Port, 0, len(names))
	for _, n := range names {
		out = append(out, Port{Name: n})
	}
	return out, nil
}
