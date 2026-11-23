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
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/config"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/security/crypto"
)

// keyMigrationSetting records which file key the stored credentials were last
// moved off, so the move runs once rather than at every start-up.
const keyMigrationSetting = "crypto_file_key_migrated"

// keepFileMasterKeyReadable handles a db_config.yaml whose crypto_master_key
// differs from the key that wins at start-up (--master-key or
// HERMOD_MASTER_KEY, which a package install sets in hermod.env).
//
// The setup wizard and key rotation used to save a key of their own to the
// file and encrypt with it until the next restart; the restart then brought
// the environment's key back and everything saved in between stopped
// decrypting. The file's key is added as a decrypt-only key so those values
// open again. It returns that key, or "" when there is nothing to recover.
func keepFileMasterKeyReadable(logger hermod.Logger) string {
	pinned := config.PinnedMasterKey()
	if pinned == "" || !config.IsDBConfigured() {
		return ""
	}
	fileKey, err := config.FileMasterKey()
	if err != nil || fileKey == "" || fileKey == pinned {
		return ""
	}
	crypto.AddDecryptKey(fileKey)
	logger.Warn("db_config.yaml holds a crypto_master_key different from the one in use " +
		"(--master-key or HERMOD_MASTER_KEY). Values saved under the file's key are still read, " +
		"and are moved to the key in use. Keep db_config.yaml: backups taken before this may need it.")
	return fileKey
}

// keyMigrationStore is the part of storage moving credentials between keys needs.
type keyMigrationStore interface {
	ReEncryptSecrets(ctx context.Context, newKey string) error
	GetSetting(ctx context.Context, key string) (string, error)
	SaveSetting(ctx context.Context, key, value string) error
}

// moveOffFileMasterKey re-encrypts every stored credential and secret under the
// key in use, so nothing depends on the file's key any more. It runs once for
// each pair of keys. ReEncryptSecrets writes all or nothing, so if a value
// opens under neither key nothing changes and every value that opened before
// still opens.
func moveOffFileMasterKey(ctx context.Context, store keyMigrationStore, fileKey string, logger hermod.Logger) {
	if store == nil || fileKey == "" {
		return
	}
	pinned := config.PinnedMasterKey()
	if pinned == "" {
		return
	}
	mark := keyPairFingerprint(fileKey, pinned)
	if done, err := store.GetSetting(ctx, keyMigrationSetting); err == nil && done == mark {
		return
	}
	if err := store.ReEncryptSecrets(ctx, pinned); err != nil {
		logger.Warn("Could not move stored credentials to the master key in use; they are still "+
			"read under either key", "error", err)
		return
	}
	if err := store.SaveSetting(ctx, keyMigrationSetting, mark); err != nil {
		logger.Warn("Moved stored credentials to the master key in use but could not record it; "+
			"it will run again at the next start-up", "error", err)
		return
	}
	logger.Info("Moved stored credentials saved under db_config.yaml's crypto_master_key to the master key in use")
}

// keyPairFingerprint names a pair of keys without revealing either.
func keyPairFingerprint(from, to string) string {
	sum := sha256.Sum256([]byte("hermod-master-key-migration\x00" + from + "\x00" + to))
	return hex.EncodeToString(sum[:])
}

// masterKeyReadsTheData reports whether start-up may go on: false only when the
// key check proves the key in force cannot read what is stored. A database that
// cannot be asked yet is not a reason to stop.
func masterKeyReadsTheData(ctx context.Context, store storage.SettingStore, logger hermod.Logger) bool {
	err := storage.VerifyMasterKey(ctx, store)
	if errors.Is(err, storage.ErrWrongMasterKey) {
		logger.Error("Refusing to start: the crypto master key in use cannot read the credentials " +
			"and secrets this database stores, and starting would save new ones under it. Put back " +
			"the key the data was saved under (--master-key, HERMOD_MASTER_KEY in " +
			"/etc/hermod/hermod.env on a package install, or crypto_master_key in db_config.yaml), " +
			"then change keys only through key rotation.")
		return false
	}
	if err != nil {
		logger.Warn("Could not check the crypto master key against the stored data", "error", err)
	}
	return true
}
