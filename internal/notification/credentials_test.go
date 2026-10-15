// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package notification

import (
	"strings"
	"testing"
)

// channelSecret stands in for a token: distinctive, so a match can only mean
// the credential itself reached the error text.
const channelSecret = "S3CR3Tsentinel"

// TestChannelErrorsDoNotCarryCredentials fails every webhook-style channel and
// reads what Settings → Test reports for it.
//
// Each of these channels authenticates through its URL: the Telegram token is
// part of the request path, a Slack or Discord webhook URL is the credential
// itself, and a generic webhook often carries a token in its query. net/http
// quotes the URL in a failed request's error, so the unmodified error handed
// the secret to the browser here, and to the log table — "Notification channel
// failed" — whenever a real alert could not be delivered.
func TestChannelErrorsDoNotCarryCredentials(t *testing.T) {
	prev := telegramAPIBase
	telegramAPIBase = "http://127.0.0.1:1"
	t.Cleanup(func() { telegramAPIBase = prev })

	// As typed, and pasted with a trailing newline, which fails URL parsing
	// and is quoted by the parse error instead.
	for _, secret := range []string{channelSecret, channelSecret + "\n"} {
		// Port 1 needs root to bind, so nothing answers there.
		ns := NotificationSettings{
			TelegramToken:  "123456:" + secret,
			TelegramChatID: "42",
			SlackWebhook:   "http://127.0.0.1:1/services/T000/B000/" + secret,
			DiscordWebhook: "http://127.0.0.1:1/api/webhooks/1/" + secret,
			WebhookURL:     "http://127.0.0.1:1/hook?token=" + secret,

			PanmailURL:        "http://127.0.0.1:1",
			PanmailAPIKey:     secret,
			PanmailProviderID: "prov-1",
			PanmailFrom:       "alerts@example.com",
			PanmailTo:         "ops@example.com",
		}

		tested := 0
		for _, r := range ns.Test(t.Context()) {
			if r.Channel == "email" {
				continue // no SMTP host configured, so it is skipped
			}
			tested++
			if r.Status != "error" {
				t.Errorf("%s: status %q against an address nothing listens on, so its failure was never examined", r.Channel, r.Status)
				continue
			}
			if strings.Contains(r.Error, channelSecret) {
				t.Errorf("%s: error carries the credential: %s", r.Channel, r.Error)
			}
		}
		if tested != 5 {
			t.Errorf("examined %d channels, want 5 (telegram, slack, discord, webhook, panmail)", tested)
		}
	}
}
