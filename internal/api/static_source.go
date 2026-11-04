// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package api

// serveUIFromDisk says whether the UI comes from internal/api/static on disk
// rather than the copy built into the binary. The built-in copy wins unless a
// dev run asks for the folder (HERMOD_DEV=true, which scripts/dev.sh sets so
// a rebuilt bundle shows without relinking) or the binary was built without
// one. A folder found in the working directory is not enough on its own: an
// old checkout there once served a pre-upgrade UI from an upgraded install.
func serveUIFromDisk(dev string, embedded, onDisk bool) bool {
	if !onDisk {
		return false
	}
	return dev == "true" || !embedded
}
