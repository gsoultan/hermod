package config

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// LoadDBConfig is on the hot path.
//
// The auth middleware calls it once per authenticated request to get the JWT
// signing secret, so every API call was reading and parsing a YAML file from
// disk — about 14µs, two thirds of the middleware's total work.
//
// Caching it is only safe if nothing that can change at runtime gets frozen.
// These tests pin the three ways it can change: the file is edited, an
// interpolated ${VAR} changes, or an override variable changes. The speedup is
// worth nothing if any of them stops being picked up.
// ---------------------------------------------------------------------------

// configDir points LoadDBConfig at a scratch directory holding content.
func configDir(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	writeConfig(t, dir, content)
	t.Setenv("HERMOD_CONFIG_DIR", dir)
	return dir
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "db_config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
}

// TestAnEditIsPickedUp: the file is the source of truth, and editing it has to
// take effect without a restart, exactly as before.
func TestAnEditIsPickedUp(t *testing.T) {
	dir := configDir(t, "type: sqlite\nconn: first\n")

	cfg, err := LoadDBConfig()
	if err != nil {
		t.Fatalf("LoadDBConfig: %v", err)
	}
	if cfg.Conn != "first" {
		t.Fatalf("conn = %q, want first", cfg.Conn)
	}

	// Same length, so only the modification time distinguishes them — the case a
	// size check alone would miss.
	writeConfig(t, dir, "type: sqlite\nconn: SECOND\n")

	cfg, err = LoadDBConfig()
	if err != nil {
		t.Fatalf("LoadDBConfig: %v", err)
	}
	if cfg.Conn != "SECOND" {
		t.Errorf("conn = %q after the file changed, want SECOND; an edit is not being picked up", cfg.Conn)
	}
}

// TestAnInterpolatedVariableIsPickedUp. The file can reference ${VAR}, so the
// parsed result depends on the environment as well as on the bytes. A cache
// keyed only on the file would serve a stale value here.
func TestAnInterpolatedVariableIsPickedUp(t *testing.T) {
	configDir(t, "type: sqlite\nconn: ${HERMOD_TEST_CONN}\n")
	t.Setenv("HERMOD_TEST_CONN", "before")

	cfg, err := LoadDBConfig()
	if err != nil {
		t.Fatalf("LoadDBConfig: %v", err)
	}
	if cfg.Conn != "before" {
		t.Fatalf("conn = %q, want before", cfg.Conn)
	}

	t.Setenv("HERMOD_TEST_CONN", "after")

	cfg, err = LoadDBConfig()
	if err != nil {
		t.Fatalf("LoadDBConfig: %v", err)
	}
	if cfg.Conn != "after" {
		t.Errorf("conn = %q after the variable changed, want after; "+
			"an interpolated value was cached with the file", cfg.Conn)
	}
}

// TestAnOverrideIsPickedUp: overrides have highest precedence and are applied
// after the file is parsed, so they must never be part of what gets cached.
func TestAnOverrideIsPickedUp(t *testing.T) {
	configDir(t, "type: sqlite\nconn: from-file\njwt_secret: from-file\n")

	if cfg, err := LoadDBConfig(); err != nil || cfg.JWTSecret != "from-file" {
		t.Fatalf("cfg = %+v, err = %v", cfg, err)
	}

	t.Setenv("HERMOD_JWT_SECRET", "from-env")

	cfg, err := LoadDBConfig()
	if err != nil {
		t.Fatalf("LoadDBConfig: %v", err)
	}
	if cfg.JWTSecret != "from-env" {
		t.Errorf("jwt_secret = %q, want from-env; the override was frozen into the cache", cfg.JWTSecret)
	}
}

// TestCallersCannotCorruptWhatIsCached. LoadDBConfig returns a pointer, and
// callers do mutate it — rotating the master key loads, edits and saves. Handing
// out the cached struct itself would let one caller's edit leak into every later
// read, which is the worst kind of bug to chase.
func TestCallersCannotCorruptWhatIsCached(t *testing.T) {
	configDir(t, "type: sqlite\nconn: original\njwt_secret: original\n")

	first, err := LoadDBConfig()
	if err != nil {
		t.Fatalf("LoadDBConfig: %v", err)
	}
	first.JWTSecret = "mutated by a caller"
	first.Conn = "mutated by a caller"

	second, err := LoadDBConfig()
	if err != nil {
		t.Fatalf("LoadDBConfig: %v", err)
	}
	if second.JWTSecret != "original" || second.Conn != "original" {
		t.Errorf("a caller's mutation leaked into a later load: %+v", second)
	}
}

// TestTheFileIsNotReparsedWhenNothingChanged is the point of the exercise.
func TestTheFileIsNotReparsedWhenNothingChanged(t *testing.T) {
	configDir(t, "type: sqlite\nconn: steady\njwt_secret: s\n")

	if _, err := LoadDBConfig(); err != nil {
		t.Fatalf("LoadDBConfig: %v", err)
	}
	before := dbConfigParses.Load()

	for range 50 {
		if _, err := LoadDBConfig(); err != nil {
			t.Fatalf("LoadDBConfig: %v", err)
		}
	}

	if got := dbConfigParses.Load() - before; got != 0 {
		t.Errorf("50 unchanged loads reparsed the file %d times, want 0; "+
			"every authenticated API request pays for this", got)
	}
}

// TestAMissingFileStillErrors: the caller distinguishes "no config" from "empty
// config", and caching must not blur that.
func TestAMissingFileStillErrors(t *testing.T) {
	t.Setenv("HERMOD_CONFIG_DIR", t.TempDir())

	if _, err := LoadDBConfig(); err == nil {
		t.Error("a missing config file loaded without error")
	}
}
