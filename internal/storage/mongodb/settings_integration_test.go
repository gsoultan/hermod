// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

//go:build integration
// +build integration

package mongodb

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/storage/configsecrets"
	"github.com/gsoultan/hermod/pkg/security/crypto"
)

// The ML worker connected from the console, against a live MongoDB: its
// setting round-trips, and its token survives a master-key rotation, as the
// SQL store's does (internal/storage/sql/reencrypt_test.go).
func TestMongoMLWorkerSettingSurvivesRotation(t *testing.T) {
	s, _ := newTraceMongo(t)
	t.Cleanup(func() { crypto.SetMasterKey(crypto.DefaultMasterKey) })
	crypto.SetMasterKey("ml-worker-setting-old-key")
	ctx := t.Context()

	stored, _ := json.Marshal(configsecrets.Encrypt(map[string]string{"url": "http://hermod-ml:8090", "token": "s3cret"}))
	if err := s.SaveSetting(ctx, storage.MLWorkerSettingKey, string(stored)); err != nil {
		t.Fatalf("SaveSetting: %v", err)
	}
	if raw, err := s.GetSetting(ctx, storage.MLWorkerSettingKey); err != nil || raw != string(stored) {
		t.Fatalf("GetSetting = %q, %v", raw, err)
	}

	if err := s.(*mongoStorage).ReEncryptSecrets(ctx, "ml-worker-setting-new-key"); err != nil {
		t.Fatalf("ReEncryptSecrets: %v", err)
	}
	crypto.SetMasterKey("ml-worker-setting-new-key")

	raw, err := s.GetSetting(ctx, storage.MLWorkerSettingKey)
	if err != nil || strings.Contains(raw, "s3cret") {
		t.Fatalf("after rotation = %q, %v", raw, err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if plain := configsecrets.Decrypt(got); plain["token"] != "s3cret" {
		t.Errorf("after rotation the connection reads %v", plain)
	}
}
