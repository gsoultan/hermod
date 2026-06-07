package config

import (
	"os"
	"path/filepath"
)

func GetConfigDir() string {
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
