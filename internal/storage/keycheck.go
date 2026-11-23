// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/gsoultan/hermod/pkg/security/crypto"
)

// KeyCheckSetting holds a known value encrypted under the master key, so
// start-up can tell whether the key in force is the one the data was saved
// under.
const KeyCheckSetting = "crypto_key_check"

const keyCheckPlaintext = "hermod-master-key-check-v1"

// ErrWrongMasterKey means the master key in force cannot read what this
// database stores.
var ErrWrongMasterKey = errors.New("the master key in use cannot read the stored data")

// SettingStore is the part of storage the key check needs.
type SettingStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	SaveSetting(ctx context.Context, key, value string) error
}

// VerifyMasterKey checks the key in force against the recorded key check, and
// records one when there is none yet.
//
// Starting under the wrong key is worse than not starting: every value saved
// from then on is encrypted under it, so the data ends up split across two
// keys, and putting the right key back no longer fixes everything. Hermod
// refuses to start on ErrWrongMasterKey for that reason.
func VerifyMasterKey(ctx context.Context, s SettingStore) error {
	stored, err := s.GetSetting(ctx, KeyCheckSetting)
	if err != nil {
		return fmt.Errorf("reading the master key check: %w", err)
	}
	if stored == "" {
		enc, err := crypto.Encrypt(keyCheckPlaintext)
		if err != nil {
			return fmt.Errorf("recording the master key check: %w", err)
		}
		if err := s.SaveSetting(ctx, KeyCheckSetting, enc); err != nil {
			return fmt.Errorf("recording the master key check: %w", err)
		}
		return nil
	}
	if plain, err := crypto.Decrypt(stored); err != nil || plain != keyCheckPlaintext {
		return ErrWrongMasterKey
	}
	return nil
}

// WriteKeyCheck records the key check under key. Rotation calls it once the
// data has moved to the new key.
func WriteKeyCheck(ctx context.Context, s SettingStore, key string) error {
	enc, err := crypto.EncryptWith(key, keyCheckPlaintext)
	if err != nil {
		return err
	}
	return s.SaveSetting(ctx, KeyCheckSetting, enc)
}
