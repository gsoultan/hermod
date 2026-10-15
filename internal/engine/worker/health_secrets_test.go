// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package worker

import (
	"testing"

	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

// A health ping builds its connector from the stored config with the secret
// references resolved, as the running connector is, and without writing into
// the stored map. It used the stored values as written, so a source whose
// password was secret:NAME pinged with the text "secret:NAME" and was shown
// down.
func TestHealthPingsResolveConnectorSecrets(t *testing.T) {
	t.Setenv("HERMOD_SECRET_PING_PW", "pw")
	t.Cleanup(func() { evaluator.SetSecretSource(nil) })
	w := &Worker{registry: registry.NewRegistry(nil)}

	stored := map[string]string{"password": "secret:PING_PW", "dsn": "u:{{secret:PING_PW}}@h"}
	cfg, err := w.resolveConfig(t.Context(), "", stored)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["password"] != "pw" || cfg["dsn"] != "u:pw@h" {
		t.Errorf("resolved %v", cfg)
	}
	cfg["password"] = "changed"
	if stored["password"] != "secret:PING_PW" {
		t.Error("the stored config was modified")
	}
}
