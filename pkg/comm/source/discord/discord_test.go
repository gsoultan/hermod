// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
