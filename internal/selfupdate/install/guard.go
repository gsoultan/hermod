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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

// The boot guard.
//
// Install leaves a marker beside the binary. Every start of the process
// counts itself against it, and a start that stays up long enough is
// confirmed, which removes it. A new version that keeps dying before it is
// confirmed - a crash on start, a panic in a migration, a listener that cannot
// bind - is put back after DefaultBootAttempts tries, by whichever start finds
// the count exhausted. Under systemd or launchd that happens with no one
// watching; run by hand, the next start does it.
//
// Rollback restores the binary only. A database migration the new version ran
// is not undone, which is one reason automatic installs stay within a major
// version.

// DefaultBootAttempts is how many starts a new version gets.
const DefaultBootAttempts = 3

// Result is how an update ended.
type Result string

const (
	ResultUpdated    Result = "updated"
	ResultRolledBack Result = "rolled_back"
)

// Marker is a pending update: installed, not yet confirmed.
type Marker struct {
	From     string    `json:"from"`
	To       string    `json:"to"`
	At       time.Time `json:"at"`
	Attempts int       `json:"attempts"`
}

// Outcome is how the last update ended, kept so the UI can say so after the
// restart.
type Outcome struct {
	Result Result    `json:"result"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

func markerPath(exe string) string  { return exe + ".update.json" }
func outcomePath(exe string) string { return exe + ".last-update.json" }

func readMarker(exe string) (*Marker, error) {
	b, err := os.ReadFile(markerPath(exe))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Marker
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("unreadable update marker %s: %w", markerPath(exe), err)
	}
	return &m, nil
}

func writeMarker(exe string, m *Marker) error { return writeJSON(markerPath(exe), m) }

func removeMarker(exe string) error {
	if err := os.Remove(markerPath(exe)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// writeJSON writes through a temporary file and a rename, so a crash never
// leaves half a marker for the next start to misread.
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// GuardBoot counts this start against a pending update, and rolls the update
// back when it has had maxAttempts starts without being confirmed. It returns
// true when it rolled back, in which case the caller must restart into the
// restored binary instead of carrying on.
//
// running is the version of the process calling it. A marker for some other
// version is stale - the swap never happened, or someone replaced the binary
// by hand since - and is dropped rather than counted, so it can never roll
// back a binary it was not written for.
func GuardBoot(exe, running string, maxAttempts int) (bool, error) {
	m, err := readMarker(exe)
	if err != nil || m == nil {
		return false, err
	}
	if !isVersion(running, m.To) {
		return false, removeMarker(exe)
	}
	if m.Attempts >= maxAttempts {
		detail := fmt.Sprintf("%s did not stay up through %d starts", m.To, m.Attempts)
		return true, restore(exe, m, detail)
	}
	m.Attempts++
	return false, writeMarker(exe, m)
}

// Confirm records that the new version is up and disarms the guard. With
// nothing pending, or a pending update to a version other than running, it
// does nothing: an older process still up while a newer binary waits on disk
// must not confirm a version it is not.
func Confirm(exe, running string) error {
	m, err := readMarker(exe)
	if err != nil || m == nil || !isVersion(running, m.To) {
		return err
	}
	if err := writeJSON(outcomePath(exe), Outcome{Result: ResultUpdated, From: m.From, To: m.To, At: time.Now().UTC()}); err != nil {
		return err
	}
	return removeMarker(exe)
}

// Rollback puts the previous binary back on request.
func Rollback(exe, reason string) error {
	m, err := readMarker(exe)
	if err != nil {
		return err
	}
	if m == nil {
		m = &Marker{}
		if o := LastOutcome(exe); o != nil && o.Result == ResultUpdated {
			m.From, m.To = o.From, o.To
		}
	}
	return restore(exe, m, reason)
}

func restore(exe string, m *Marker, detail string) error {
	prev := PreviousPath(exe)
	if _, err := os.Stat(prev); err != nil {
		return fmt.Errorf("no previous binary to roll back to at %s: %w", prev, err)
	}
	if err := os.Rename(prev, exe); err != nil {
		return fmt.Errorf("cannot restore %s: %w", prev, err)
	}
	if err := writeJSON(outcomePath(exe), Outcome{Result: ResultRolledBack, From: m.From, To: m.To, Detail: detail, At: time.Now().UTC()}); err != nil {
		return err
	}
	return removeMarker(exe)
}

// isVersion reports whether a build string ("v1.25.0 (Enterprise Edition)")
// is the release tag.
func isVersion(build, tag string) bool {
	return tag != "" && slices.Contains(strings.Fields(build), tag)
}

// LastOutcome is how the last update ended, or nil if none is recorded.
func LastOutcome(exe string) *Outcome {
	b, err := os.ReadFile(outcomePath(exe))
	if err != nil {
		return nil
	}
	var o Outcome
	if json.Unmarshal(b, &o) != nil {
		return nil
	}
	return &o
}

// Pending reports the update awaiting confirmation, if any.
func Pending(exe string) *Marker {
	m, _ := readMarker(exe)
	return m
}
