// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
