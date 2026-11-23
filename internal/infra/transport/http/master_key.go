// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import "github.com/gsoultan/hermod/internal/config"

// masterKeyForSetup is the key setup saves and installs. A key set outside
// db_config.yaml (--master-key or HERMOD_MASTER_KEY) wins at every start-up,
// so setup uses it too: a different key saved to the file would only be in
// force until the next restart, and everything saved under it until then
// would become unreadable.
func masterKeyForSetup(requested string) string {
	if pinned := config.PinnedMasterKey(); pinned != "" {
		return pinned
	}
	return requested
}

// pinnedKeyRefusal explains why the master key cannot be rotated from the
// API, or is empty when it can.
func pinnedKeyRefusal() string {
	if config.PinnedMasterKey() == "" {
		return ""
	}
	return "the master key is set by HERMOD_MASTER_KEY (or --master-key), which wins over " +
		"db_config.yaml at every start-up, so a key rotated here would be dropped at the next " +
		"restart and the data re-encrypted under it could no longer be read; no key was changed"
}
