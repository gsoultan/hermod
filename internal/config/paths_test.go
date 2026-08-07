package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Without an override the config directory is fixed at ~/.hermod, so any
// development or test run writes over the db_config.yaml the developer is
// already using. HERMOD_CONFIG_DIR lets a dev stack keep an isolated config —
// and incidentally makes this package testable without the monkey patching the
// test below laments.
func TestGetConfigDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory available: %v", err)
	}
	defaultDir := filepath.Join(home, ".hermod")
	override := filepath.Join(t.TempDir(), "isolated")

	tests := []struct {
		name string
		env  string
		want string
	}{
		{"defaults to ~/.hermod when unset", "", defaultDir},
		{"honours HERMOD_CONFIG_DIR", override, override},
		{"ignores a blank override", "   ", defaultDir},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HERMOD_CONFIG_DIR", tc.env)
			if got := GetConfigDir(); got != tc.want {
				t.Errorf("GetConfigDir() = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestGetConfigPathUsesOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERMOD_CONFIG_DIR", dir)

	want := filepath.Join(dir, "db_config.yaml")
	if got := GetConfigPath("db_config.yaml"); got != want {
		t.Errorf("GetConfigPath() = %q; want %q", got, want)
	}
}

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
