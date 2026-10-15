// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package json

import (
	"testing"
)

func TestJSONFormatter_NilMessage(t *testing.T) {
	f := NewJSONFormatter()
	f.Mode = ModeFull
	_, err := f.Format(nil)
	if err == nil {
		t.Error("Expected error for nil message, got nil")
	}
}
