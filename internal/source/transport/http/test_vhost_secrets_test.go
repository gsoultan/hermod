// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
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

type pingSource struct{ hermod.Source }

func (pingSource) Ping(context.Context) error { return nil }
func (pingSource) Close() error               { return nil }

// Test connection on a source reads the secrets of the source's vhost, as the
// running source does. It did not: a RabbitMQ password saved as a vhost secret
// ("dev@1234") failed the test while the same password typed in passed.
func TestTestConnectionResolvesTheSourcesVHostSecrets(t *testing.T) {
	store := vhostSecretStorage{secrets: map[string]string{"sales/RMQ_PASS": "dev@1234"}}
	reg := registry.NewRegistry(store)
	var got factory.SourceConfig
	reg.SetFactories(func(cfg factory.SourceConfig) (hermod.Source, error) {
		got = cfg
		return pingSource{}, nil
	}, nil)
	mux := http.NewServeMux()
	(&SourceHandler{Handler: &handlers.Handler{Storage: store, Registry: reg}}).RegisterSourceRoutes(mux)

	send := func(user *storage.User) *httptest.ResponseRecorder {
		body := `{"type":"rabbitmq_queue","vhost":"sales","config":{"host":"mq","username":"app","password":"{{secret.RMQ_PASS}}","queue_name":"q"}}`
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/sources/test", strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, user))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := send(&storage.User{Role: storage.RoleEditor, VHosts: []string{"sales"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("test connection answered %d: %s", rec.Code, rec.Body.String())
	}
	if got.VHost != "sales" {
		t.Errorf("source built for vhost %q, want sales", got.VHost)
	}
	// The "@" in the password is escaped, exactly as a typed one is.
	if url := factory.BuildConnectionString(got.Config, got.Type); url != "amqp://app:dev%401234@mq:5672/" {
		t.Errorf("connection URL = %q", url)
	}
	if rec := send(&storage.User{Role: storage.RoleEditor, VHosts: []string{"other"}}); rec.Code != http.StatusForbidden {
		t.Errorf("a caller outside the vhost was answered %d, want 403", rec.Code)
	}
}
