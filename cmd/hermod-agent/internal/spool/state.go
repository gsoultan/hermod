// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package spool

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

const stateFile = "state.json"

// state is what the spool must remember beyond its segments: how far the
// server has acknowledged, and how many records were dropped for space.
type state struct {
	Acked   uint64 `json:"acked"`
	Dropped uint64 `json:"dropped"`
}

func loadState(dir string) (state, error) {
	var st state
	b, err := os.ReadFile(filepath.Join(dir, stateFile)) //nolint:gosec // G304: the spool's own directory
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(b, &st)
}

// saveState writes the state durably: a temporary file, synced, renamed over
// the old one, then the directory synced so the rename itself survives a
// power cut.
func saveState(dir string, st state) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, stateFile+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // G304: the spool's own directory
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, stateFile)); err != nil {
		return err
	}
	return syncDir(dir)
}

// syncDir makes a create, rename or remove in dir durable. Windows cannot
// open a directory for syncing and makes those durable itself, so an error
// there is not a failure.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir) //nolint:gosec // G304: the spool's own directory
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}
