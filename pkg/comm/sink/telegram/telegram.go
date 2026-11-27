// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package telegram

import (
	"context"
	"fmt"

	"github.com/gsoultan/hermod"
)

// TelegramSink implements the hermod.Sink interface for Telegram.
type TelegramSink struct {
	token     string
	chatID    string
	threadID  int64
	formatter hermod.Formatter
	baseURL   string
}

// NewTelegramSink creates a new TelegramSink.
func NewTelegramSink(token, chatID string, formatter hermod.Formatter) *TelegramSink {
	return &TelegramSink{
		token:     token,
		chatID:    chatID,
		formatter: formatter,
		baseURL:   DefaultBaseURL,
	}
}

// SetMessageThreadID makes the sink post into one topic of a forum group.
// Without it a message lands in the group's General topic. Empty clears it.
func (s *TelegramSink) SetMessageThreadID(topicID string) error {
	id, err := parseThreadID(topicID)
	if err != nil {
		return err
	}
	s.threadID = id
	return nil
}

// Write sends a message to Telegram.
func (s *TelegramSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	var data []byte
	var err error

	if s.formatter != nil {
		data, err = s.formatter.Format(msg)
	} else {
		data = msg.Payload()
	}

	if err != nil {
		return fmt.Errorf("failed to format message: %w", err)
	}

	body := map[string]any{
		"chat_id":    s.chatID,
		"text":       string(data),
		"parse_mode": "Markdown",
	}
	if s.threadID != 0 {
		body["message_thread_id"] = s.threadID
	}
	// The refusal carries the fix ("the person must press Start"), so a
	// failed run says what to do, not only Telegram's two words.
	return callAPI(ctx, s.baseURL, s.token, "sendMessage", body, nil)
}

func (s *TelegramSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err := s.Write(ctx, msg); err != nil {
				return err
			}
		}
	}
	return nil
}

// Ping checks the connection to Telegram API.
func (s *TelegramSink) Ping(ctx context.Context) error {
	if err := callAPI(ctx, s.baseURL, s.token, "getMe", nil, nil); err != nil {
		return fmt.Errorf("invalid telegram token or connection error: %w", err)
	}
	return nil
}

// Close closes the Telegram sink.
func (s *TelegramSink) Close() error {
	return nil
}

// SetBaseURL overrides the Bot API root this sink talks to.
//
// The host was baked into a format string, so the sink could not be exercised
// without dialling Telegram — and the conformance suite did exactly that, with
// a dummy token, on every run. An empty string leaves the default in place.
func (s *TelegramSink) SetBaseURL(u string) {
	if u != "" {
		s.baseURL = u
	}
}
