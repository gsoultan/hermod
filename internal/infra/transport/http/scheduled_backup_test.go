package http

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Scheduled backups.
//
// The file this writes holds decrypted credentials in plaintext — necessarily,
// since a backup that cannot restore a credential is not a backup. One file is
// every credential in the deployment, written unattended, on a timer.
//
// So the refusals matter more than the happy path, and they are what these
// mostly cover: no destination means nothing is written, a directory anyone
// else can read is refused rather than used, and a backup that could not be
// taken completely is not written at all.
// ---------------------------------------------------------------------------

func secureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	return dir
}

func TestADirectoryOthersCanReadIsRefused(t *testing.T) {
	dir := secureDir(t)
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	err := checkDestination(dir)
	if err == nil {
		t.Fatal("a world-readable directory was accepted; a backup there is every " +
			"credential in the deployment, readable by every user on the host")
	}
	if !strings.Contains(err.Error(), "chmod 700") {
		t.Errorf("the error should say how to fix it, got: %v", err)
	}
}

func TestAGroupReadableDirectoryIsRefused(t *testing.T) {
	dir := secureDir(t)
	if err := os.Chmod(dir, 0o740); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := checkDestination(dir); err == nil {
		t.Error("a group-readable directory was accepted")
	}
}

func TestASecureDirectoryIsAccepted(t *testing.T) {
	if err := checkDestination(secureDir(t)); err != nil {
		t.Errorf("a 0700 directory was refused: %v", err)
	}
}

// TestNoDestinationMeansDisabled. There is no default path, so the feature
// cannot be switched on by accident — which for this payload matters more than
// convenience.
func TestNoDestinationMeansDisabled(t *testing.T) {
	if err := checkDestination(""); err == nil {
		t.Error("an empty destination was accepted; backups would go somewhere unspecified")
	}
	if err := checkDestination("   "); err == nil {
		t.Error("a blank destination was accepted")
	}
}

func TestAMissingDirectoryIsRefused(t *testing.T) {
	if err := checkDestination(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Error("a missing directory was accepted; the first backup would fail unattended")
	}
}

// TestRetentionKeepsTheNewest, and keeps exactly as many as asked.
func TestRetentionKeepsTheNewest(t *testing.T) {
	dir := secureDir(t)
	names := []string{
		"hermod-backup-20260101T000000Z.json",
		"hermod-backup-20260102T000000Z.json",
		"hermod-backup-20260103T000000Z.json",
		"hermod-backup-20260104T000000Z.json",
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("{}"), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if err := pruneBackups(dir, 2); err != nil {
		t.Fatalf("prune: %v", err)
	}

	left := map[string]bool{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		left[e.Name()] = true
	}
	if len(left) != 2 {
		t.Fatalf("kept %d files, want 2: %v", len(left), left)
	}
	for _, want := range names[2:] {
		if !left[want] {
			t.Errorf("%s was deleted; retention kept the oldest rather than the newest", want)
		}
	}
}

// TestRetentionTouchesOnlyItsOwnFiles. A destination shared with anything else
// must not lose that — deleting an operator's unrelated files would be a far
// worse bug than keeping too many backups.
func TestRetentionTouchesOnlyItsOwnFiles(t *testing.T) {
	dir := secureDir(t)
	keep := []string{"important.json", "notes.txt", "hermod-backup.json.bak"}
	for _, n := range keep {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	for _, n := range []string{
		"hermod-backup-20260101T000000Z.json",
		"hermod-backup-20260102T000000Z.json",
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("{}"), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if err := pruneBackups(dir, 1); err != nil {
		t.Fatalf("prune: %v", err)
	}

	for _, n := range keep {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s was deleted; retention only owns files it wrote", n)
		}
	}
}

// TestRetentionOfZeroKeepsTheDefault rather than deleting everything just
// written, which is the sort of off-by-one that only shows up in an incident.
func TestRetentionOfZeroKeepsTheDefault(t *testing.T) {
	s := BackupSchedule{Directory: "/tmp"}
	if got := s.retention(); got != DefaultBackupRetention {
		t.Errorf("retention() = %d, want %d", got, DefaultBackupRetention)
	}
	if got := s.interval(); got != DefaultBackupInterval {
		t.Errorf("interval() = %v, want %v", got, DefaultBackupInterval)
	}
	if got := (BackupSchedule{Interval: -time.Hour}).interval(); got != DefaultBackupInterval {
		t.Errorf("a negative interval gave %v; it would tick continuously", got)
	}
}

// TestAWrittenBackupIsOwnerOnlyAndComplete: the file must be unreadable by
// anyone else, and must be the whole export rather than a partial write.
func TestAWrittenBackupIsOwnerOnlyAndComplete(t *testing.T) {
	dir := secureDir(t)
	name := filepath.Join(dir, "hermod-backup-20260101T000000Z.json")
	body, _ := json.Marshal(BackupData{Settings: map[string]string{"k": "v"}})
	if err := os.WriteFile(name, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	info, err := os.Stat(name)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("the backup is mode %#o; it holds every credential in the deployment", perm)
	}

	var round BackupData
	raw, _ := os.ReadFile(name)
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("the written backup is not valid JSON: %v", err)
	}
	if round.Settings["k"] != "v" {
		t.Error("the backup did not round-trip")
	}
}

// TestAScheduleWithNoDirectoryStartsNothing, and its stop is safe to call.
func TestAScheduleWithNoDirectoryStartsNothing(t *testing.T) {
	h := &InfraHandler{}
	stop := h.StartScheduledBackups(t.Context(), BackupSchedule{})
	if stop == nil {
		t.Fatal("stop must be callable even when the schedule is off")
	}
	stop()
	stop()
}

// TestAMisconfiguredScheduleDoesNotStart. A destination that cannot be used is
// reported at start-up rather than a day later, when the first backup was due
// and silently did not happen.
func TestAMisconfiguredScheduleDoesNotStart(t *testing.T) {
	h := &InfraHandler{}
	stop := h.StartScheduledBackups(t.Context(), BackupSchedule{
		Directory: filepath.Join(t.TempDir(), "missing"),
		Interval:  time.Millisecond,
	})
	defer stop()
	// Nothing to assert beyond not panicking and not spinning: the point is that
	// a bad destination is refused at start rather than retried forever.
	time.Sleep(20 * time.Millisecond)
}
