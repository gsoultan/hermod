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
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// errNoChat is returned for a test send with no chat to send to.
var errNoChat = errors.New("telegram: a chat ID is needed: pick a chat, or type its ID")

// Sent is the chat a test message reached, as Telegram names it.
type Sent struct {
	Chat
	MessageID int64 `json:"message_id"`
}

// SendTestMessage sends text to a chat, in a topic when topicID is set, and
// reports the chat Telegram delivered it to.
//
// Test connection only calls getMe, which proves the token and nothing about
// the chat: a sink with a chat ID the bot could never post to passed it. This
// sends for real, so a success means the message is in the chat, and a refusal
// carries the same hint a run would.
func SendTestMessage(ctx context.Context, baseURL, token, chatID, topicID, text string) (Sent, error) {
	token, chatID = strings.TrimSpace(token), strings.TrimSpace(chatID)
	if token == "" {
		return Sent{}, ErrNoToken
	}
	if chatID == "" {
		return Sent{}, errNoChat
	}
	body := map[string]any{"chat_id": chatID, "text": text}
	if err := addThread(body, topicID); err != nil {
		return Sent{}, err
	}
	var result struct {
		MessageID int64  `json:"message_id"`
		Chat      tgChat `json:"chat"`
	}
	if err := callAPI(ctx, baseURL, token, "sendMessage", body, &result); err != nil {
		return Sent{}, err
	}
	return Sent{Chat: chatFrom(result.Chat), MessageID: result.MessageID}, nil
}

// parseThreadID reads a topic ID. Empty means no topic.
func parseThreadID(topicID string) (int64, error) {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(topicID, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("telegram: topic ID %q is not a topic number; pick the topic again, or leave it empty to post in General", topicID)
	}
	return id, nil
}

func addThread(body map[string]any, topicID string) error {
	id, err := parseThreadID(topicID)
	if err != nil {
		return err
	}
	if id != 0 {
		body["message_thread_id"] = id
	}
	return nil
}
