// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package selfupdate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Snapshotter copies the metadata database to a new file.
type Snapshotter interface {
	SnapshotTo(ctx context.Context, path string) error
}

const (
	snapshotPrefix = "hermod-before-update-"
	// keepSnapshots is how many pre-update copies stay on disk.
	keepSnapshots = 3
)

// SnapshotDatabase writes a copy of the database into dir before an update
// from version from, and returns its path. The folder is made private: the
// copy holds the stored credentials, still encrypted with the master key.
// Only the newest keepSnapshots copies are kept.
func SnapshotDatabase(ctx context.Context, db Snapshotter, dir, from string, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("cannot make the backup folder %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: a folder needs the execute bit to be entered; 0700 is owner-only.
		return "", fmt.Errorf("cannot make the backup folder %s private: %w", dir, err)
	}
	if from == "" {
		from = "unknown"
	}
	path := filepath.Join(dir, snapshotPrefix+from+"-"+now.UTC().Format("20060102T150405Z")+".db")
	if err := db.SnapshotTo(ctx, path); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("cannot copy the database to %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("cannot make %s private: %w", path, err)
	}
	pruneSnapshots(dir)
	return path, nil
}

// pruneSnapshots removes all but the newest copies. Names carry the time in a
// sortable form; files not named by SnapshotDatabase are never touched.
func pruneSnapshots(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var ours []os.DirEntry
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasPrefix(e.Name(), snapshotPrefix) && strings.HasSuffix(e.Name(), ".db") {
			ours = append(ours, e)
		}
	}
	slices.SortFunc(ours, func(a, b os.DirEntry) int { return strings.Compare(stamp(a.Name()), stamp(b.Name())) })
	for len(ours) > keepSnapshots {
		_ = os.Remove(filepath.Join(dir, ours[0].Name()))
		ours = ours[1:]
	}
}

// stamp is the time part of a copy's name, which sorts in time order whatever
// version precedes it.
func stamp(name string) string {
	name = strings.TrimSuffix(name, ".db")
	return name[strings.LastIndex(name, "-")+1:]
}

// Copier is a metadata store that may be able to copy itself: a SQLite one
// can, a server database cannot.
type Copier interface {
	Snapshotter
	CanSnapshot() bool
}

// PreUpdateBackup is what is backed up before an update. There are no down
// migrations, so going back from a release that upgraded the schema means
// restoring one of these.
type PreUpdateBackup struct {
	// DB is the metadata storage; nil before setup.
	DB any
	// Dir is where copies of a SQLite database go.
	Dir string
	// AppBackup writes the scheduled configuration backup, when one is
	// configured in AppBackupDir; nil otherwise.
	AppBackup    func(ctx context.Context) (string, error)
	AppBackupDir string
}

func (b PreUpdateBackup) copier() Copier {
	c, ok := b.DB.(Copier)
	if !ok || !c.CanSnapshot() {
		return nil
	}
	return c
}

// Plan says, before anyone installs, what will be backed up.
func (b PreUpdateBackup) Plan() string {
	if b.DB == nil {
		return ""
	}
	var parts []string
	if b.copier() != nil {
		parts = append(parts, "Before installing, Hermod copies its database to "+b.Dir+" (the newest three copies are kept)")
	} else {
		parts = append(parts, "Hermod cannot copy its database, which is a server database (PostgreSQL, MySQL or MongoDB). Back it up before installing: a new version can upgrade the schema, and the version before it will not start on an upgraded one")
	}
	if b.AppBackup != nil {
		parts = append(parts, "a configuration backup is written to "+b.AppBackupDir)
	}
	return strings.Join(parts, ", and ") + "."
}

// Take backs up before an update from version from, and returns where the
// backups are. Any failure stops the update.
func (b PreUpdateBackup) Take(ctx context.Context, from string, now time.Time) (string, error) {
	var where []string
	if b.AppBackup != nil {
		p, err := b.AppBackup(ctx)
		if err != nil {
			return "", err
		}
		where = append(where, p)
	}
	if c := b.copier(); c != nil {
		p, err := SnapshotDatabase(ctx, c, b.Dir, from, now)
		if err != nil {
			return "", err
		}
		where = append([]string{p}, where...)
	}
	return strings.Join(where, "; "), nil
}
