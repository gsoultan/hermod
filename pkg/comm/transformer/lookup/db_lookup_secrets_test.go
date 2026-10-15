// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package lookup

import (
	"context"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
)

type resolvingRegistry struct{ vhost string }

func (r *resolvingRegistry) ResolveSecrets(_ context.Context, vhost string, config map[string]string) (map[string]string, error) {
	r.vhost = vhost
	out := map[string]string{}
	for k, v := range config {
		if v == "secret:PW" {
			v = "pw"
		}
		out[k] = v
	}
	return out, nil
}

// The Mongo path of DB Lookup reads its source's credentials resolved, for
// the source's own vhost, as the SQL path does.
func TestDBLookupResolvesTheMongoSourcesSecrets(t *testing.T) {
	reg := &resolvingRegistry{}
	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	cfg, err := resolvedSourceConfig(ctx, storage.Source{VHost: "sales", Config: map[string]string{"password": "secret:PW", "host": "m"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg["password"] != "pw" || cfg["host"] != "m" || reg.vhost != "sales" {
		t.Errorf("cfg = %v, vhost = %q", cfg, reg.vhost)
	}
}
