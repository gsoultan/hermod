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
	"testing"

	"github.com/gsoultan/hermod/pkg/security/crypto"
)

type memSettings map[string]string

func (m memSettings) GetSetting(_ context.Context, key string) (string, error) { return m[key], nil }
func (m memSettings) SaveSetting(_ context.Context, key, value string) error {
	m[key] = value
	return nil
}

// A server that starts under a key that cannot read what it stored would
// encrypt every new value under that key too, leaving data split across two
// keys. That is what made the production incident hard to undo. The key check
// catches it at start-up, before anything is written.
func TestVerifyMasterKeyCatchesAChangedKey(t *testing.T) {
	t.Cleanup(func() { crypto.SetMasterKey(crypto.DefaultMasterKey) })
	s := memSettings{}

	crypto.SetMasterKey("first-key-0123456789abcdef")
	if err := VerifyMasterKey(context.Background(), s); err != nil {
		t.Fatalf("first start-up: %v", err)
	}
	if s[KeyCheckSetting] == "" {
		t.Fatal("the first start-up did not record a key check")
	}
	if err := VerifyMasterKey(context.Background(), s); err != nil {
		t.Fatalf("same key again: %v", err)
	}

	crypto.SetMasterKey("someone-changed-hermod-env-0123")
	if err := VerifyMasterKey(context.Background(), s); !errors.Is(err, ErrWrongMasterKey) {
		t.Fatalf("VerifyMasterKey under another key = %v, want ErrWrongMasterKey", err)
	}
}

// Rotation moves the check to the new key along with everything else.
func TestWriteKeyCheckFollowsRotation(t *testing.T) {
	t.Cleanup(func() { crypto.SetMasterKey(crypto.DefaultMasterKey) })
	s := memSettings{}
	crypto.SetMasterKey("old-key-0123456789abcdef")
	if err := VerifyMasterKey(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if err := WriteKeyCheck(context.Background(), s, "new-key-0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	crypto.SetMasterKey("new-key-0123456789abcdef")
	if err := VerifyMasterKey(context.Background(), s); err != nil {
		t.Fatalf("after rotation: %v", err)
	}
}
