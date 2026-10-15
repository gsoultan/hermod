// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
