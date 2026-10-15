// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package registry

import (
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
)

// Every field of a source and a sink can hold secret and env references, in
// the middle of a value as well as as the whole of it, and they are read for
// the connector's own vhost before the factory sees the config.
func TestConnectorConfigsResolveSecretAndEnvReferencesAnywhere(t *testing.T) {
	t.Setenv("HERMOD_ENV_PG_HOST", "db.internal")
	reg, _ := newSecretRegistry(t)

	var gotSource, gotSink map[string]string
	reg.SetFactories(
		func(cfg factory.SourceConfig) (hermod.Source, error) {
			gotSource = cfg.Config
			return &arraySource{count: 1}, nil
		},
		func(cfg factory.SinkConfig) (hermod.Sink, error) {
			gotSink = cfg.Config
			return &pipeSink{name: "out"}, nil
		},
	)

	cfg := map[string]string{
		"dsn":      "postgres://app:{{secret:API_KEY}}@{{env:PG_HOST}}:5432/app",
		"token":    `Bearer {{secret("ONLY_B")}}`,
		"url":      "https://api/{{.after.id}}",
		"password": "secret:API_KEY",
		"gateway":  "https://{{env.PG_HOST}}/{{secret.ONLY_B}}",
	}
	if _, err := reg.createSource(t.Context(), factory.SourceConfig{ID: "s", Type: "test", VHost: "tenant-b", Config: cfg}); err != nil {
		t.Fatalf("createSource: %v", err)
	}
	if _, err := reg.createSink(t.Context(), factory.SinkConfig{ID: "k", Type: "test", VHost: "tenant-b", Config: cfg}); err != nil {
		t.Fatalf("createSink: %v", err)
	}

	want := map[string]string{
		"dsn":      "postgres://app:b-key@db.internal:5432/app",
		"token":    "Bearer b-only",
		"url":      "https://api/{{.after.id}}",
		"password": "b-key",
		"gateway":  "https://db.internal/b-only",
	}
	for name, got := range map[string]map[string]string{"source": gotSource, "sink": gotSink} {
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s %s = %q, want %q", name, k, got[k], v)
			}
		}
	}
	if cfg["dsn"] != "postgres://app:{{secret:API_KEY}}@{{env:PG_HOST}}:5432/app" {
		t.Errorf("the stored config was modified: %q", cfg["dsn"])
	}
}

// A connector whose reference resolves to nothing is not built: it fails
// naming the reference, instead of connecting with the token as a password.
func TestConnectorWithAnUnresolvedReferenceIsNotBuilt(t *testing.T) {
	reg, _ := newSecretRegistry(t)
	built := false
	reg.SetFactories(
		func(cfg factory.SourceConfig) (hermod.Source, error) { built = true; return &arraySource{}, nil },
		func(cfg factory.SinkConfig) (hermod.Sink, error) { built = true; return &pipeSink{}, nil },
	)
	// ONLY_B is tenant-b's; tenant-a cannot read it.
	cfg := map[string]string{"password": "{{secret:ONLY_B}}"}

	_, err := reg.createSource(t.Context(), factory.SourceConfig{ID: "s", Type: "test", VHost: "tenant-a", Config: cfg})
	if err == nil || !strings.Contains(err.Error(), `secret "ONLY_B"`) || !strings.Contains(err.Error(), "password") {
		t.Errorf("createSource err = %v, want one naming the field and the reference", err)
	}
	_, err = reg.createSink(t.Context(), factory.SinkConfig{ID: "k", Type: "test", VHost: "tenant-a", Config: cfg})
	if err == nil || !strings.Contains(err.Error(), `secret "ONLY_B"`) {
		t.Errorf("createSink err = %v, want one naming the reference", err)
	}
	if built {
		t.Error("a connector was built with an unresolved reference")
	}
}
