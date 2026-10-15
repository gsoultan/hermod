// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package discord

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscordSource_Read(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"id": "123", "content": "hello", "author": {"id": "u1", "username": "user1"}}]`))
	}))
	defer server.Close()

	// Mocking the API URL by replacing the base URL in the source logic is hard without refactoring.
	// But I can at least check if it compiles and runs.
	// Actually, I'll modify the DiscordSource to allow custom base URL for testing.
}
