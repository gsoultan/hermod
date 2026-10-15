// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package telemetry

import (
	"bytes"
	"errors"
	"testing"

	"github.com/rs/zerolog"
)

func TestDefaultLogger_ErrorHandling(t *testing.T) {
	var buf bytes.Buffer
	zl := zerolog.New(&buf)
	l := &DefaultLogger{logger: zl}

	err := errors.New("test error")
	l.Error("failed", "error", err)

	output := buf.String()
	if !contains(output, "test error") {
		t.Errorf("Expected output to contain 'test error', got: %s", output)
	}
	if !contains(output, "failed") {
		t.Errorf("Expected output to contain 'failed', got: %s", output)
	}
}

func contains(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}
