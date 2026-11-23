// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package config

import (
	"os"
	"sync/atomic"
)

var pinnedMasterKey atomic.Pointer[string]

// SetPinnedMasterKey records a master key given on the command line, which,
// like HERMOD_MASTER_KEY, wins over db_config.yaml at every start-up.
func SetPinnedMasterKey(key string) {
	pinnedMasterKey.Store(&key)
}

// PinnedMasterKey is the master key set outside db_config.yaml: the
// --master-key flag, else HERMOD_MASTER_KEY. Empty means the file decides.
//
// When it is set, saving another key to db_config.yaml changes nothing past
// the next restart, so the setup wizard and key rotation must not do it.
func PinnedMasterKey() string {
	if k := pinnedMasterKey.Load(); k != nil && *k != "" {
		return *k
	}
	return os.Getenv("HERMOD_MASTER_KEY")
}

// FileMasterKey is the crypto_master_key db_config.yaml itself holds, with no
// environment override applied. A missing file holds none.
func FileMasterKey() (string, error) {
	cfg, err, parseErr := dbConfigFromFile(getDBConfigPath())
	if parseErr != nil {
		return "", parseErr
	}
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return cfg.CryptoMasterKey, nil
}
