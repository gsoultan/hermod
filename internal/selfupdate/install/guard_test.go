// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package install

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// installed leaves exe in the state a successful Install does: the new
// binary in place, the old one beside it, and the guard armed.
func installed(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "hermod")
	if err := os.WriteFile(exe, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PreviousPath(exe), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeMarker(exe, &Marker{From: "v1.24.9", To: "v1.25.0", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return exe
}

const newBuild = "v1.25.0 (Enterprise Edition)"

func TestStaleMarkerIsDroppedNotCounted(t *testing.T) {
	// The old binary starting with a marker for the new one means the swap
	// never happened: counting it would roll back a binary that was never
	// replaced.
	exe := installed(t)
	for range 5 {
		if rolled, err := GuardBoot(exe, "v1.24.9 (Enterprise Edition)", 3); err != nil || rolled {
			t.Fatalf("GuardBoot from the old version = %v, %v", rolled, err)
		}
	}
	if m, _ := readMarker(exe); m != nil {
		t.Fatalf("stale marker kept: %+v", m)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatalf("binary = %q, want it untouched", b)
	}
}

func TestOlderProcessDoesNotConfirmNewerBinary(t *testing.T) {
	exe := installed(t)
	if err := Confirm(exe, "v1.24.9 (Enterprise Edition)"); err != nil {
		t.Fatal(err)
	}
	if m, _ := readMarker(exe); m == nil {
		t.Fatal("the old process confirmed an update it is not running")
	}
}

func TestGuardBootWithoutPendingUpdateDoesNothing(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "hermod")
	rolled, err := GuardBoot(exe, newBuild, 3)
	if err != nil || rolled {
		t.Fatalf("GuardBoot with no update = %v, %v", rolled, err)
	}
}

func TestGuardBootRollsBackAfterRepeatedFailedStarts(t *testing.T) {
	exe := installed(t)
	for i := 1; i <= 3; i++ {
		rolled, err := GuardBoot(exe, newBuild, 3)
		if err != nil || rolled {
			t.Fatalf("start %d: GuardBoot = %v, %v; want a further try", i, rolled, err)
		}
	}
	// The fourth start means the new version never stayed up long enough to
	// be confirmed three times running.
	rolled, err := GuardBoot(exe, newBuild, 3)
	if err != nil || !rolled {
		t.Fatalf("GuardBoot after 3 failed starts = %v, %v; want a rollback", rolled, err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Fatalf("binary after rollback = %q, want the previous one", b)
	}
	if m, _ := readMarker(exe); m != nil {
		t.Fatalf("marker survived the rollback: %+v", m)
	}
	o := LastOutcome(exe)
	if o == nil || o.Result != ResultRolledBack || o.From != "v1.24.9" || o.To != "v1.25.0" {
		t.Fatalf("outcome = %+v, want a rollback from v1.25.0 to v1.24.9 recorded", o)
	}
}

func TestConfirmRecordsSuccessAndDisarmsGuard(t *testing.T) {
	exe := installed(t)
	if _, err := GuardBoot(exe, newBuild, 3); err != nil {
		t.Fatal(err)
	}
	if err := Confirm(exe, newBuild); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if m, _ := readMarker(exe); m != nil {
		t.Fatalf("marker survived confirmation: %+v", m)
	}
	o := LastOutcome(exe)
	if o == nil || o.Result != ResultUpdated || o.To != "v1.25.0" {
		t.Fatalf("outcome = %+v", o)
	}
	// Further starts are ordinary starts.
	for range 5 {
		if rolled, _ := GuardBoot(exe, newBuild, 3); rolled {
			t.Fatal("a confirmed update was rolled back")
		}
	}
	if err := Confirm(exe, newBuild); err != nil {
		t.Fatalf("Confirm with nothing pending: %v", err)
	}
}

func TestRollbackRestoresPreviousOnRequest(t *testing.T) {
	exe := installed(t)
	if err := Confirm(exe, newBuild); err != nil {
		t.Fatal(err)
	}
	if err := Rollback(exe, "asked for"); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Fatalf("binary after rollback = %q", b)
	}
	if o := LastOutcome(exe); o == nil || o.Result != ResultRolledBack {
		t.Fatalf("outcome = %+v", o)
	}
	if err := Rollback(exe, "again"); err == nil {
		t.Fatal("a second rollback with no previous binary succeeded")
	}
}
