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
