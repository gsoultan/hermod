// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package retrieve

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// A vector store that is rate limiting answers 429. The step failed the
// message; it is busy now, with the store's wait, and the engine waits it out.
func TestRetrieve_PineconeRateLimitIsBusy(t *testing.T) {
	var inputs []string
	pc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "6")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(pc.Close)

	cfg := pineconeConfig(embedServer(t, &inputs), pc.URL, map[string]any{"targetField": "context"})
	_, err := (&Transformer{}).Transform(t.Context(), msgWith(t, map[string]any{"question": "q"}), cfg)
	if !errors.Is(err, hermod.ErrBusy) {
		t.Fatalf("err = %v, want busy", err)
	}
	if d, ok := hermod.RetryAfter(err); !ok || d != 6*time.Second {
		t.Errorf("RetryAfter = %v, %v; want the 6s the store asked for", d, ok)
	}
}
