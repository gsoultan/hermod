// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package factory

import (
	"testing"

	"github.com/gsoultan/hermod"
)

// The Telegram form writes bot_token (NotificationSinkConfig.tsx) and the
// factory read token, so the token typed into the form never reached the sink:
// every message went to https://api.telegram.org/bot/sendMessage, which is a
// 404 about a bot nobody has.
func TestChatBotToken_ReadsTheKeyTheFormWrites(t *testing.T) {
	if got := chatBotToken(hermod.StringMap{"bot_token": "123456:ABC"}); got != "123456:ABC" {
		t.Errorf("token = %q, want the form's bot_token", got)
	}
}

// A sink stored or imported against the factory's own name still works.
func TestChatBotToken_StillReadsTheBareKey(t *testing.T) {
	if got := chatBotToken(hermod.StringMap{"token": "123456:ABC"}); got != "123456:ABC" {
		t.Errorf("token = %q, want the bare token", got)
	}
}

func TestChatBotToken_PrefersTheFormKey(t *testing.T) {
	if got := chatBotToken(hermod.StringMap{"token": "old", "bot_token": "new"}); got != "new" {
		t.Errorf("token = %q, want %q", got, "new")
	}
}

func TestCreateSink_TelegramBuilds(t *testing.T) {
	snk, err := createSinkBase(SinkConfig{
		ID:   "telegram-1",
		Type: "telegram",
		Config: hermod.StringMap{
			"bot_token": "123456:ABC",
			"chat_id":   "-100123456789",
			"template":  "Order {{.id}}",
		},
	})
	if err != nil {
		t.Fatalf("building the sink failed: %v", err)
	}
	if snk == nil {
		t.Fatal("no sink was built")
	}
	_ = snk.Close()
}
