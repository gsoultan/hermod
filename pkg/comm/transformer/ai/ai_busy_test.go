// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package ai

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// A model API that is rate limiting this key answers 429 with the wait it
// wants. The step reported it as a plain failure, so every message that met a
// rate limit failed -- the commonest answer an AI API gives under load. It is
// busy now, with the API's wait, and the engine waits it out.
func TestAITransformerReportsARateLimitAsBusy(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		t.Run(provider, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "4")
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			t.Cleanup(srv.Close)

			msg := message.AcquireMessage()
			t.Cleanup(msg.Release)
			msg.SetData("text", "hi")
			_, err := (&AITransformer{}).Transform(t.Context(), msg, map[string]any{
				"provider": provider, "endpoint": srv.URL, "model": "m", "prompt": "p",
			})
			if !errors.Is(err, hermod.ErrBusy) {
				t.Fatalf("err = %v, want busy", err)
			}
			if d, ok := hermod.RetryAfter(err); !ok || d != 4*time.Second {
				t.Errorf("RetryAfter = %v, %v; want the 4s the API asked for", d, ok)
			}
		})
	}
}
