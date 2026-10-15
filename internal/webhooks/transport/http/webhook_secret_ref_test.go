// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
