package http

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// A configuration file that does not exist yet is not a server error.
//
// A fresh instance has no config.yaml — it is written the first time someone
// saves settings. All eight settings handlers loaded it before doing anything,
// so ENOENT became a 500 and the first thing a new administrator saw on the
// Settings page was a red "failed to load configuration" toast. The page whose
// purpose is to create that file reported its absence as a fault, and the save
// handlers could not write the first config either, because they load before
// they store.
//
// Found by the layout audit against a running stack: the toast was flagged as
// rendering off-screen, and the reason it existed at all turned out to matter
// more than where it was drawn.
func TestAnAbsentConfigFileReadsAsEmptyRatherThanFailing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "config.yaml")

	cfg, err := loadConfigOrEmpty(missing)
	if err != nil {
		t.Fatalf("a config file that has not been created yet reported an error: %v\n"+
			"this is what made a fresh instance answer 500 on every settings read", err)
	}
	if cfg == nil {
		t.Fatal("no error, but no configuration either; the handlers dereference this")
	}
}

// A file that exists and cannot be parsed is still an error. Substituting
// defaults there would let the next save overwrite something deliberate.
func TestAnUnparseableConfigFileIsStillAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("\tthis: is: not: valid: yaml\n\t\t- [}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := loadConfigOrEmpty(path); err == nil {
		t.Error("a corrupt config file was reported as an empty one; the next save " +
			"would overwrite a file somebody meant to keep")
	} else if errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a corrupt file was misreported as a missing one: %v", err)
	}
}

// A real file round-trips, so the helper is not simply swallowing everything.
func TestAnExistingConfigFileIsStillRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("state_store:\n  type: sqlite\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfigOrEmpty(path)
	if err != nil {
		t.Fatalf("an existing, valid config file failed to load: %v", err)
	}
	if cfg.StateStore.Type != "sqlite" {
		t.Errorf("the file was read but its contents were lost: state_store.type=%q, want sqlite",
			cfg.StateStore.Type)
	}
}
