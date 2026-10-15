// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
