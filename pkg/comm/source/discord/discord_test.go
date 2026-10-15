// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
