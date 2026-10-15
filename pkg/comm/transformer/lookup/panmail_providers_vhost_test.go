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
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

// panmailScopedSecrets answers per vhost: "vhost/key" -> value.
type panmailScopedSecrets map[string]string

func (s panmailScopedSecrets) Get(ctx context.Context, key string) (string, error) {
	return s.GetScoped(ctx, "", key)
}

func (s panmailScopedSecrets) GetScoped(_ context.Context, vhost, key string) (string, error) {
	return s[vhost+"/"+key], nil
}

// The key is resolved against no row data, so a row cannot choose where a
// tenant credential is sent. It still has to be the key of the vhost the
// workflow runs in: {{secret("PANMAIL_API_KEY")}} is what the form recommends,
// and a vhost that saved its own key must be the one whose key is sent.
func TestPanmailProvidersSendsTheKeyOfTheMessagesVHost(t *testing.T) {
	t.Cleanup(func() { evaluator.SetSecretSource(nil) })
	evaluator.SetSecretSource(panmailScopedSecrets{
		"tenant-a/PANMAIL_API_KEY": "a-key",
		"tenant-b/PANMAIL_API_KEY": "b-key",
	})

	for vhost, want := range map[string]string{"tenant-a": "a-key", "tenant-b": "b-key"} {
		t.Run(vhost, func(t *testing.T) {
			g := newPanmailGateway(t, 200, twoProviders)
			tr, reg := newPanmailProvidersFixture()
			msg := message.AcquireMessage()
			t.Cleanup(msg.Release)
			msg.SetVHost(vhost)
			// A row naming a key of its own changes nothing.
			msg.SetData("PANMAIL_API_KEY", "from-the-row")

			ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
			if _, err := tr.Transform(ctx, msg, map[string]any{
				"baseUrl": g.srv.URL,
				"apiKey":  `{{secret("PANMAIL_API_KEY")}}`,
			}); err != nil {
				t.Fatalf("Transform: %v", err)
			}
			if got := g.apiKey.Load(); got != want {
				t.Errorf("X-API-Key = %v, want %q", got, want)
			}
		})
	}
}
