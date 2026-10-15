// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
