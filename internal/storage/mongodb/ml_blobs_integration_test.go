// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
