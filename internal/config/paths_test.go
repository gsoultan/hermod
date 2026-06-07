package config

import (
	"os"
	"testing"
)

func TestEnsureConfigDir(t *testing.T) {
	// We can't easily mock UserHomeDir without monkey patching,
	// but we can test if it creates a directory.

	// Create a temporary directory and override GetConfigDir behavior for testing if possible
	// Actually, EnsureConfigDir calls GetConfigDir.

	dir := GetConfigDir()

	// If the dir already exists, we should probably check if it handles that (it does)
	err := EnsureConfigDir()
	if err != nil {
		t.Fatalf("EnsureConfigDir failed: %v", err)
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Errorf("Directory %s was not created", dir)
	}
}
