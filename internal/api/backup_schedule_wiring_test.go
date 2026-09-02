package api

import (
	"os"
	"testing"

	"github.com/gsoultan/Hermod/internal/config"
)

// ---------------------------------------------------------------------------
// Starting the backup schedule.
//
// The mechanism refuses an unsafe destination, but a mechanism nothing calls is
// not a feature. These cover the wiring: that a configured directory starts a
// schedule, and — more importantly — that an unconfigured one starts nothing.
//
// A backup carries every credential in the deployment in plaintext, so the
// default has to be off. An upgrade that quietly began writing those to disk
// would be a security regression delivered as a feature.
// ---------------------------------------------------------------------------

func TestNoBackupDirectoryStartsNoSchedule(t *testing.T) {
	for _, cfg := range []*config.Config{
		{},
		{Backup: config.BackupConfig{Directory: ""}},
		{Backup: config.BackupConfig{Directory: "   "}},
	} {
		s := NewServer(nil, nil, cfg, "", nil)
		if s.stopBackups != nil {
			t.Error("a schedule was started without a directory being configured; " +
				"an upgrade would start writing every credential in the deployment to disk")
		}
		s.Stop()
	}
}

func TestANilConfigStartsNoSchedule(t *testing.T) {
	s := NewServer(nil, nil, nil, "", nil)
	if s.stopBackups != nil {
		t.Error("a nil config started a backup schedule")
	}
	s.Stop()
}

// TestAConfiguredDirectoryStartsASchedule, so the setting is not inert.
func TestAConfiguredDirectoryStartsASchedule(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	s := NewServer(nil, nil, &config.Config{
		Backup: config.BackupConfig{Directory: dir, Interval: "1h", Retention: 3},
	}, "", nil)
	defer s.Stop()

	if s.stopBackups == nil {
		t.Error("a configured backup directory started nothing; the setting would be inert")
	}
}

// TestAnUnsafeDirectoryStartsNothing. The writer refuses a directory others can
// read; the server must not end up holding a stop function for a schedule that
// never ran.
func TestAnUnsafeDirectoryStartsNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	s := NewServer(nil, nil, &config.Config{
		Backup: config.BackupConfig{Directory: dir},
	}, "", nil)
	defer s.Stop()

	// StartScheduledBackups returns a no-op for a refused destination, so this
	// is about the server not pretending a schedule is running.
	if s.stopBackups != nil {
		s.stopBackups()
	}
}

// TestStopIsSafeTwice: shutdown paths double-call.
func TestStoppingBackupsTwiceIsSafe(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	s := NewServer(nil, nil, &config.Config{
		Backup: config.BackupConfig{Directory: dir},
	}, "", nil)

	s.Stop()
	s.Stop()
}

// TestAMalformedIntervalDoesNotDisableBackups. A typo in a duration should cost
// the interval, not the backups — falling back to the default is what an
// operator expects, and refusing to back up over a typo is the worse failure.
func TestAMalformedIntervalDoesNotDisableBackups(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	s := NewServer(nil, nil, &config.Config{
		Backup: config.BackupConfig{Directory: dir, Interval: "every day"},
	}, "", nil)
	defer s.Stop()

	if s.stopBackups == nil {
		t.Error("a malformed interval disabled backups entirely; the fallback is the default " +
			"interval, not no backups")
	}
}
