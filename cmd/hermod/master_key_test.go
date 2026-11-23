// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gsoultan/hermod/internal/config"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
	"github.com/gsoultan/hermod/pkg/security/crypto"
)

// A .deb install reads its master key from HERMOD_MASTER_KEY in hermod.env.
// The setup wizard used to save a different, generated key to db_config.yaml
// and encrypt with it until the first restart. After that restart the
// environment's key won and every credential and secret saved before it failed
// with "cannot decrypt with the configured master key". Start-up must keep
// those values readable.
func TestStartUpStillOpensDataSavedUnderTheFileKey(t *testing.T) {
	const fileKey = "wizard-generated-key-0123456789"
	const envKey = "key-from-hermod-env-0123456789"

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db_config.yaml"),
		[]byte("type: sqlite\nconn: "+filepath.Join(dir, "h.db")+"\ncrypto_master_key: "+fileKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMOD_CONFIG_DIR", dir)
	t.Setenv("HERMOD_MASTER_KEY", envKey)
	t.Cleanup(func() {
		crypto.SetMasterKey(crypto.DefaultMasterKey)
		config.SetPinnedMasterKey("")
	})

	crypto.SetMasterKey(fileKey)
	saved, err := crypto.Encrypt("s3cret")
	if err != nil {
		t.Fatal(err)
	}

	o := &Options{masterKey: envKey}
	initMasterKey(o)
	getStorageConfig(o)
	if got := keepFileMasterKeyReadable(telemetry.NewDefaultLogger()); got != fileKey {
		t.Fatalf("keepFileMasterKeyReadable = %q, want the file's key", got)
	}

	got, err := crypto.Decrypt(saved)
	if err != nil {
		t.Fatalf("a value saved under db_config.yaml's key no longer opens: %v", err)
	}
	if got != "s3cret" {
		t.Fatalf("Decrypt = %q", got)
	}
}

type fakeKeyStore struct {
	settings  map[string]string
	reEncrypt []string
	fail      error
}

func (f *fakeKeyStore) ReEncryptSecrets(_ context.Context, newKey string) error {
	f.reEncrypt = append(f.reEncrypt, newKey)
	return f.fail
}

func (f *fakeKeyStore) GetSetting(_ context.Context, key string) (string, error) {
	return f.settings[key], nil
}

func (f *fakeKeyStore) SaveSetting(_ context.Context, key, value string) error {
	f.settings[key] = value
	return nil
}

// Once the values open again they are moved to the key in use, once.
func TestStartUpMovesDataOffTheFileKeyOnce(t *testing.T) {
	t.Setenv("HERMOD_MASTER_KEY", "key-from-hermod-env-0123456789")
	store := &fakeKeyStore{settings: map[string]string{}}
	logger := telemetry.NewDefaultLogger()

	moveOffFileMasterKey(context.Background(), store, "wizard-generated-key-0123456789", logger)
	moveOffFileMasterKey(context.Background(), store, "wizard-generated-key-0123456789", logger)

	if len(store.reEncrypt) != 1 || store.reEncrypt[0] != "key-from-hermod-env-0123456789" {
		t.Fatalf("ReEncryptSecrets calls = %q, want one, under the key in use", store.reEncrypt)
	}
}

// A failed move is not recorded, so the next start-up tries again.
func TestAFailedMoveIsRetriedAtTheNextStartUp(t *testing.T) {
	t.Setenv("HERMOD_MASTER_KEY", "key-from-hermod-env-0123456789")
	store := &fakeKeyStore{settings: map[string]string{}, fail: errors.New("boom")}
	logger := telemetry.NewDefaultLogger()

	moveOffFileMasterKey(context.Background(), store, "wizard-generated-key-0123456789", logger)
	store.fail = nil
	moveOffFileMasterKey(context.Background(), store, "wizard-generated-key-0123456789", logger)

	if len(store.reEncrypt) != 2 {
		t.Fatalf("ReEncryptSecrets calls = %d, want 2", len(store.reEncrypt))
	}
}

func TestNothingMovesWhenTheKeysAgree(t *testing.T) {
	t.Setenv("HERMOD_MASTER_KEY", "key-from-hermod-env-0123456789")
	store := &fakeKeyStore{settings: map[string]string{}}
	moveOffFileMasterKey(context.Background(), store, "", telemetry.NewDefaultLogger())
	if len(store.reEncrypt) != 0 {
		t.Fatalf("ReEncryptSecrets called with no file key to move off")
	}
}

// Start-up refuses to run under a key that cannot read the stored data, so no
// value is ever saved under the wrong key next to ones under the right key.
func TestStartUpRefusesAKeyThatCannotReadTheData(t *testing.T) {
	t.Cleanup(func() { crypto.SetMasterKey(crypto.DefaultMasterKey) })
	store := &fakeKeyStore{settings: map[string]string{}}
	logger := telemetry.NewDefaultLogger()

	crypto.SetMasterKey("the-key-the-data-is-under-0123")
	if !masterKeyReadsTheData(context.Background(), store, logger) {
		t.Fatal("refused the key the data is under")
	}
	crypto.SetMasterKey("a-key-someone-put-in-hermod-env")
	if masterKeyReadsTheData(context.Background(), store, logger) {
		t.Fatal("started under a key that cannot read the stored data")
	}
}
