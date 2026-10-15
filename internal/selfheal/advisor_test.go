// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package selfheal_test

import (
	"testing"

	"github.com/gsoultan/hermod/internal/selfheal"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestAdvisorIsOffUnlessConfigured(t *testing.T) {
	adv, err := selfheal.AdvisorFromEnv(env(nil), newStore())
	if err != nil || adv != nil {
		t.Fatalf("advisor %v, err %v", adv, err)
	}
}

func TestAdvisorRefusesAPlaintextKey(t *testing.T) {
	adv, err := selfheal.AdvisorFromEnv(env(map[string]string{
		selfheal.EnvProvider: "openai",
		selfheal.EnvAPIKey:   "sk-plain",
	}), newStore())
	if err == nil || adv != nil {
		t.Fatalf("advisor %v, err %v", adv, err)
	}
}

func TestAdvisorAcceptsASecretReference(t *testing.T) {
	adv, err := selfheal.AdvisorFromEnv(env(map[string]string{
		selfheal.EnvProvider: "openai",
		selfheal.EnvAPIKey:   `{{secret("OPENAI_API_KEY")}}`,
	}), newStore())
	if err != nil || adv == nil {
		t.Fatalf("advisor %v, err %v", adv, err)
	}
}
