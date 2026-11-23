// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

//go:build linux || windows || (darwin && cgo)

package detect

import "go.bug.st/serial/enumerator"

// SystemPorts lists serial ports with their USB details.
func SystemPorts() ([]Port, error) {
	list, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return nil, err
	}
	out := make([]Port, 0, len(list))
	for _, d := range list {
		out = append(out, Port{
			Name:         d.Name,
			IsUSB:        d.IsUSB,
			VID:          d.VID,
			PID:          d.PID,
			SerialNumber: d.SerialNumber,
			Product:      d.Product,
		})
	}
	return out, nil
}
