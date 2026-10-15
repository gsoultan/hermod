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
