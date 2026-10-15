// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

//go:build integration
// +build integration

package mongodb

import (
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/storage/mlblobtest"
)

// The same contract the SQL store keeps (internal/storage/sql/ml_blobs_test.go),
// against a live MongoDB.
func TestMongoMLBlobs(t *testing.T) {
	s, _ := newTraceMongo(t)
	st, ok := s.(storage.MLBlobStore)
	if !ok {
		t.Fatal("the MongoDB store does not implement storage.MLBlobStore")
	}
	mlblobtest.Run(t, st, "tenant-a")
}
