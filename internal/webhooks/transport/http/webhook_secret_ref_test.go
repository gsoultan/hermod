// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/source/webhook"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

// A webhook's API key may be a secret reference, like every other connector
// setting. It was compared as written, so the key that opened the endpoint
// was the reference itself -- "secret:HOOK_KEY", readable by anyone who could
// see the source -- and the secret never worked.
func TestAWebhookKeyMayBeASecretReference(t *testing.T) {
	t.Setenv("HERMOD_SECRET_HOOK_KEY", "sesame")
	t.Cleanup(func() { evaluator.SetSecretSource(nil) })

	for _, ref := range []string{"secret:HOOK_KEY", "{{secret:HOOK_KEY}}", `{{secret("HOOK_KEY")}}`} {
		fullPath := "/api/webhooks/ref"
		store := sourcesOnly{sources: []storage.Source{{Type: "webhook", Config: map[string]string{"path": fullPath, "api_key": ref}}}}
		send := func(key string) int {
			mux := http.NewServeMux()
			NewWebhookHandler(&handlers.Handler{Storage: store, Registry: registry.NewRegistry(store)}).RegisterWebhookRoutes(mux)
			src := webhook.NewWebhookSource(fullPath)
			defer func() { _ = src.Close() }()
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, fullPath, strings.NewReader(webhookBody))
			req.Header.Set("X-API-Key", key)
			mux.ServeHTTP(rec, req)
			return rec.Code
		}
		if code := send(ref); code != http.StatusUnauthorized {
			t.Errorf("%s: the reference itself opened the endpoint (%d)", ref, code)
		}
		if code := send("sesame"); code != http.StatusAccepted {
			t.Errorf("%s: the secret's value was answered %d, want 202", ref, code)
		}
	}
}
