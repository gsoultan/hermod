package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Scheduled backups.
//
// The runbook said backups had to be taken by hand, which means they are taken
// until the week somebody is on holiday. This writes the same export the
// download endpoint produces, on a timer, keeping the most recent few.
//
// What it writes deserves stating plainly, because it decides every rule below:
// the export carries **decrypted credentials in plaintext** — necessarily, since
// a backup that cannot restore a credential is not a backup. One file is every
// credential in the deployment. So:
//
//   - There is no default directory. Nothing is written unless an operator names
//     a destination, which makes accidental enablement impossible.
//   - The directory must not be group- or world-accessible. A backup readable by
//     every user on the host is a credential disclosure with a schedule.
//   - Files are written 0600, through a temporary file renamed into place, so a
//     reader never sees a half-written backup and a crash never leaves one.
//
// A failure is logged and retried on the next tick rather than stopping the
// service. A backup system that takes the process down with it has made
// availability worse in exchange for durability.
const (
	// DefaultBackupInterval is used when a destination is configured without one.
	DefaultBackupInterval = 24 * time.Hour

	// DefaultBackupRetention is how many files are kept when unspecified.
	DefaultBackupRetention = 7

	// backupFilePrefix identifies files this writer owns. Retention only ever
	// deletes files matching it, so pointing the destination at a directory
	// holding anything else cannot delete that.
	backupFilePrefix = "hermod-backup-"
	backupFileSuffix = ".json"
)

// BackupSchedule describes when and where scheduled backups are written.
type BackupSchedule struct {
	// Directory is where files are written. Empty disables the schedule.
	Directory string
	// Interval between backups. Zero uses DefaultBackupInterval.
	Interval time.Duration
	// Retention is how many files to keep. Zero uses DefaultBackupRetention.
	Retention int
}

func (s BackupSchedule) interval() time.Duration {
	if s.Interval <= 0 {
		return DefaultBackupInterval
	}
	return s.Interval
}

func (s BackupSchedule) retention() int {
	if s.Retention <= 0 {
		return DefaultBackupRetention
	}
	return s.Retention
}

// checkDestination refuses a directory that would expose the backup.
//
// The check is on the directory rather than the file because a 0600 file in a
// world-executable directory is still enumerable, and because the operator can
// fix a directory once instead of after every write.
func checkDestination(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("no backup directory configured")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("backup directory %q is not usable: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("backup directory %q is not a directory", dir)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("backup directory %q is mode %#o and readable beyond its owner; "+
			"a backup holds every credential in this deployment in plaintext, so this "+
			"refuses rather than writing one there. chmod 700 it", dir, perm)
	}
	return nil
}

// WriteBackup takes one backup and writes it, then applies retention. It is
// exported so an operator-triggered "back up now" can use exactly the same path
// as the schedule.
func (h *InfraHandler) WriteBackup(ctx context.Context, schedule BackupSchedule) (string, error) {
	if err := checkDestination(schedule.Directory); err != nil {
		return "", err
	}

	data, err := h.CollectBackup(ctx)
	if err != nil {
		// Includes the refusal to truncate an over-large deployment. A scheduled
		// backup that silently dropped the overflow would be worse than none,
		// because it looks like one.
		return "", err
	}

	body, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("cannot serialise the backup: %w", err)
	}

	name := backupFilePrefix + time.Now().UTC().Format("20060102T150405Z") + backupFileSuffix
	final := filepath.Join(schedule.Directory, name)

	// Written to a temporary file and renamed, so a reader never sees a partial
	// backup and an interrupted write leaves nothing to mistake for one.
	tmp, err := os.CreateTemp(schedule.Directory, backupFilePrefix+"*.tmp")
	if err != nil {
		return "", fmt.Errorf("cannot create the backup file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("cannot restrict the backup file: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("cannot write the backup: %w", err)
	}
	// Flushed before the rename, so a crash cannot leave a correctly-named file
	// with nothing in it.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("cannot flush the backup: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("cannot close the backup: %w", err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		return "", fmt.Errorf("cannot put the backup in place: %w", err)
	}

	if err := pruneBackups(schedule.Directory, schedule.retention()); err != nil {
		// The backup itself succeeded, which is what matters. Retention failing
		// costs disk, not data.
		return final, fmt.Errorf("backup written to %s, but old backups could not be "+
			"pruned: %w", final, err)
	}
	return final, nil
}

// pruneBackups keeps the newest `keep` files this writer owns.
//
// Only files matching the prefix are ever considered, so a destination shared
// with anything else cannot lose that. Names sort chronologically because the
// timestamp is fixed-width UTC.
func pruneBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	var mine []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, backupFilePrefix) || !strings.HasSuffix(name, backupFileSuffix) {
			continue
		}
		mine = append(mine, name)
	}
	if len(mine) <= keep {
		return nil
	}

	sort.Sort(sort.Reverse(sort.StringSlice(mine)))
	var firstErr error
	for _, name := range mine[keep:] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// LogBackupProblem reports what the schedule did or could not do.
//
// A scheduled backup that fails quietly is the same failure as having no
// backups, discovered later, so every outcome is written down — including the
// successful ones, which is how an operator confirms the schedule is alive
// without going to look at the directory.
func (h *InfraHandler) LogBackupProblem(msg string) {
	log.Printf("%s", msg)
}

// StartScheduledBackups runs backups until the returned stop function is
// called. A schedule with no directory does nothing and returns a no-op, so the
// caller does not have to decide whether the feature is on.
func (h *InfraHandler) StartScheduledBackups(ctx context.Context, schedule BackupSchedule) (stop func()) {
	if strings.TrimSpace(schedule.Directory) == "" {
		return func() {}
	}

	// Checked once at start-up so a misconfigured destination is a start-up
	// complaint rather than a surprise a day later, when the first backup was
	// due and silently did not happen.
	if err := checkDestination(schedule.Directory); err != nil {
		h.LogBackupProblem(fmt.Sprintf("Scheduled backups are configured but disabled: %v", err))
		return func() {}
	}

	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(schedule.interval())
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				path, err := h.WriteBackup(ctx, schedule)
				if err != nil {
					h.LogBackupProblem(fmt.Sprintf("Scheduled backup failed: %v", err))
					continue
				}
				h.LogBackupProblem("Scheduled backup written to " + path)
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}
