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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fileSnapshot struct{ err error }

func (f fileSnapshot) SnapshotTo(_ context.Context, path string) error {
	if f.err != nil {
		return f.err
	}
	if _, err := os.Stat(path); err == nil {
		return errors.New("output file already exists")
	}
	return os.WriteFile(path, []byte("db"), 0o644)
}

func TestSnapshotDatabaseKeepsAPrivateCopyAndTheNewestThree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	at := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	var paths []string
	for i := range 5 {
		p, err := SnapshotDatabase(context.Background(), fileSnapshot{}, dir, "v1.25.0", at.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatalf("SnapshotDatabase: %v", err)
		}
		paths = append(paths, p)
	}
	if !strings.Contains(filepath.Base(paths[0]), "v1.25.0") {
		t.Fatalf("name %q does not say which version it was taken from", paths[0])
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
		t.Fatalf("backup folder mode %v, want 0700", info.Mode().Perm())
	}
	if info, _ := os.Stat(paths[4]); info.Mode().Perm() != 0o600 {
		t.Fatalf("copy mode %v, want 0600", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != keepSnapshots {
		t.Fatalf("%d copies kept, want %d", len(entries), keepSnapshots)
	}
	for _, old := range paths[:2] {
		if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should have been pruned", old)
		}
	}
}

func TestSnapshotDatabaseLeavesNothingWhenTheCopyFails(t *testing.T) {
	dir := t.TempDir()
	if _, err := SnapshotDatabase(context.Background(), fileSnapshot{err: errors.New("disk full")}, dir, "v1.25.0", time.Now()); err == nil {
		t.Fatal("want the copy's error")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
}

type sqliteStore struct{ fileSnapshot }

func (sqliteStore) CanSnapshot() bool { return true }

type serverStore struct{ fileSnapshot }

func (serverStore) CanSnapshot() bool { return false }

func TestPreUpdateBackupPlanAndTake(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)

	b := PreUpdateBackup{DB: sqliteStore{}, Dir: dir}
	if plan := b.Plan(); !strings.Contains(plan, "copies its database to "+dir) {
		t.Fatalf("plan = %q", plan)
	}
	got, err := b.Take(context.Background(), "v1.25.0", now)
	if err != nil || !strings.HasPrefix(got, dir) {
		t.Fatalf("Take = %q, %v; want a copy in %s", got, err, dir)
	}

	appCalled := false
	b = PreUpdateBackup{DB: serverStore{}, Dir: dir, AppBackupDir: "/srv/backups", AppBackup: func(context.Context) (string, error) {
		appCalled = true
		return "/srv/backups/hermod-backup.json", nil
	}}
	if plan := b.Plan(); !strings.Contains(plan, "Back it up before installing") || !strings.Contains(plan, "/srv/backups") {
		t.Fatalf("plan for a server database = %q, want it to ask for a database backup and name the configuration backup", plan)
	}
	if got, err := b.Take(context.Background(), "v1.25.0", now); err != nil || got != "/srv/backups/hermod-backup.json" || !appCalled {
		t.Fatalf("Take = %q, %v", got, err)
	}

	b.AppBackup = func(context.Context) (string, error) { return "", errors.New("backup directory is mode 0755") }
	if _, err := b.Take(context.Background(), "v1.25.0", now); err == nil {
		t.Fatal("a failed configuration backup must stop the update")
	}

	if (PreUpdateBackup{}).Plan() != "" {
		t.Fatal("before setup there is no database and nothing to say")
	}
}
