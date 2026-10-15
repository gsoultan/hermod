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
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

type vhostSecretStorage struct {
	storage.Storage
	storage.VHostSecretStore
	secrets map[string]string // "vhost/name"
}

func (s vhostSecretStorage) GetVHostSecret(_ context.Context, vhost, name string) (storage.VHostSecret, error) {
	v, ok := s.secrets[vhost+"/"+name]
	if !ok {
		return storage.VHostSecret{}, storage.ErrNotFound
	}
	return storage.VHostSecret{VHost: vhost, Name: name, Value: v}, nil
}

type pingSink struct{ hermod.Sink }

func (pingSink) Ping(context.Context) error { return nil }
func (pingSink) Close() error               { return nil }

// Test connection reads the secrets of the vhost the sink belongs to. The
// handler dropped the vhost the form sends, so a secret saved in the vhost
// -- {{secret("PANMAIL_GATEWAYURL")}} in a Panmail gateway -- never resolved
// and the test failed, while the same sink would have run.
func TestTestConnectionResolvesTheSinksVHostSecrets(t *testing.T) {
	t.Cleanup(func() { evaluator.SetSecretSource(nil) })
	store := vhostSecretStorage{secrets: map[string]string{"sales/PANMAIL_GATEWAYURL": "https://mail.example.com"}}
	reg := registry.NewRegistry(store)
	var got factory.SinkConfig
	reg.SetFactories(nil, func(cfg factory.SinkConfig) (hermod.Sink, error) {
		got = cfg
		return pingSink{}, nil
	})
	mux := http.NewServeMux()
	(&SinkHandler{Handler: &handlers.Handler{Storage: store, Registry: reg}}).RegisterSinkRoutes(mux)

	send := func(user *storage.User) *httptest.ResponseRecorder {
		body := `{"type":"panmail","vhost":"sales","config":{"base_url":"{{secret(\"PANMAIL_GATEWAYURL\")}}"}}`
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/sinks/test", strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, user))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := send(&storage.User{Role: storage.RoleEditor, VHosts: []string{"sales"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("test connection answered %d: %s", rec.Code, rec.Body.String())
	}
	if got.Config["base_url"] != "https://mail.example.com" || got.VHost != "sales" {
		t.Errorf("sink built with %+v, want the vhost's secret", got)
	}

	// Naming a vhost is asking for its secrets: one the caller cannot reach
	// is refused.
	if rec := send(&storage.User{Role: storage.RoleEditor, VHosts: []string{"other"}}); rec.Code != http.StatusForbidden {
		t.Errorf("a caller outside the vhost was answered %d, want 403", rec.Code)
	}
}
