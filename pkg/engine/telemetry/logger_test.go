// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
