// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package config

import (
	"os"
	"path/filepath"
	"strings"
)

// ConfigDirEnv overrides the directory Hermod reads and writes its own config
// from (db_config.yaml and friends).
//
// Without it the location is hard-wired to ~/.hermod, so running a development
// or test instance silently overwrites the configuration of whatever other
// instance the developer is using. Pointing this at a scratch directory keeps
// the two apart — see scripts/dev.sh.
const ConfigDirEnv = "HERMOD_CONFIG_DIR"

func GetConfigDir() string {
	if dir := strings.TrimSpace(os.Getenv(ConfigDirEnv)); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".hermod")
}

func GetConfigPath(filename string) string {
	return filepath.Join(GetConfigDir(), filename)
}

func EnsureConfigDir() error {
	dir := GetConfigDir()
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return os.MkdirAll(dir, 0755)
	}
	return nil
}
