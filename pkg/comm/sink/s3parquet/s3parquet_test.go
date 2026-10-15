// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package s3parquet

import (
	"testing"
)

func TestNewS3ParquetSink(t *testing.T) {
	sink, err := NewS3ParquetSink(t.Context(), "us-east-1", "bucket", "prefix", "ak", "sk", "", "{}", "", 4)
	if err != nil {
		t.Fatalf("failed to create sink: %v", err)
	}
	if sink == nil {
		t.Fatal("sink is nil")
	}
}
