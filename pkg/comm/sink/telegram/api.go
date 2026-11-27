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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gsoultan/hermod/pkg/infra/httpclient"
)

// DefaultBaseURL is the Bot API root every helper here and the sink talk to.
const DefaultBaseURL = "https://api.telegram.org"

// maxAPIResponseBytes bounds what is read from one Bot API answer. getUpdates
// returns at most 100 updates; a few megabytes is far more than that weighs.
const maxAPIResponseBytes = 4 << 20

// APIError is a refusal from the Bot API, with the fix for it.
//
// Telegram's descriptions are a few English words ("chat not found") whose fix
// depends on the kind of chat. Hint is that fix in a sentence, so a test send
// in the editor and a failed run both say what to do next. The descriptions
// are not localised, which is why matching them is safe here.
type APIError struct {
	Status      int
	Description string
	Hint        string
	// MigrateToChatID is the supergroup a group was upgraded to. Telegram
	// refuses the old ID from then on, and names the new one.
	MigrateToChatID string
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("telegram api returned status: %d, error: %s", e.Status, e.Description)
	if e.Hint != "" {
		msg += ". " + e.Hint
	}
	return msg
}

// apiAnswer is the envelope of every Bot API response.
type apiAnswer struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  struct {
		MigrateToChatID int64 `json:"migrate_to_chat_id"`
	} `json:"parameters"`
}

// callAPI calls one Bot API method and decodes its result into out. body is
// sent as JSON when it is not nil.
//
// The token is part of the URL, so a transport error is redacted before it is
// returned: net/http quotes the whole URL in its errors.
func callAPI(ctx context.Context, baseURL, token, method string, body, out any) error {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	apiURL := fmt.Sprintf("%s/bot%s/%s", strings.TrimRight(baseURL, "/"), token, method)
	httpMethod := http.MethodGet
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		httpMethod, reader = http.MethodPost, bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, httpMethod, apiURL, reader)
	if err != nil {
		return httpclient.RedactURLError(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpclient.DataClient.Do(req)
	if err != nil {
		return httpclient.RedactURLError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var answer apiAnswer
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAPIResponseBytes)).Decode(&answer); err != nil {
		if resp.StatusCode != http.StatusOK {
			return newAPIError(resp.StatusCode, apiAnswer{Description: http.StatusText(resp.StatusCode)})
		}
		return fmt.Errorf("telegram: unreadable answer to %s: %w", method, err)
	}
	if resp.StatusCode != http.StatusOK || !answer.OK {
		return newAPIError(resp.StatusCode, answer)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(answer.Result, out)
}

func newAPIError(status int, answer apiAnswer) *APIError {
	e := &APIError{Status: status, Description: answer.Description}
	if answer.Parameters.MigrateToChatID != 0 {
		e.MigrateToChatID = strconv.FormatInt(answer.Parameters.MigrateToChatID, 10)
	}
	e.Hint = hintFor(e)
	return e
}

// hints maps a fragment of Telegram's description to the fix. The first match
// wins, so the more specific fragments come first.
var hints = []struct{ fragment, hint string }{
	{"bot was blocked by the user", "This person blocked the bot. Ask them to open the bot in Telegram, unblock it and press Start."},
	{"can't initiate conversation", "A bot cannot message someone first. Ask the person to open the bot in Telegram and press Start, then try again."},
	{"bot is not a member of the channel", "Add the bot to the channel as an administrator that can post messages."},
	{"need administrator rights in the channel", "Make the bot an administrator of the channel with permission to post messages."},
	{"bot was kicked", "The bot was removed from this chat. Add the bot to the group again."},
	{"bot is not a member", "Add the bot to the group first."},
	{"not enough rights", "The bot is not allowed to post here. Give it permission to send messages, or make it an administrator."},
	{"have no rights to send", "The bot is not allowed to post here. Give it permission to send messages, or make it an administrator."},
	{"message thread not found", "That topic does not exist in this group. Pick the topic again, or leave the topic empty to post in General."},
	{"topic_closed", "That topic is closed. Reopen it, or pick another topic."},
	{"chat not found", "Telegram does not know this chat for this bot. For a person, they must open the bot and press Start. For a group or channel, add the bot to it first. Then check the chat ID."},
}

func hintFor(e *APIError) string {
	if e.MigrateToChatID != "" {
		return "This group was upgraded to a supergroup, which has a new chat ID: " + e.MigrateToChatID + ". Use that ID instead."
	}
	if e.Status == http.StatusUnauthorized {
		return "Telegram does not accept this bot token. Copy it again from @BotFather (/mybots, then API Token)."
	}
	if e.Status == http.StatusNotFound {
		return "Telegram does not recognise this bot token. Copy it again from @BotFather; it looks like 123456789:AA..."
	}
	desc := strings.ToLower(e.Description)
	for _, h := range hints {
		if strings.Contains(desc, h.fragment) {
			return h.hint
		}
	}
	if e.Status == http.StatusTooManyRequests {
		return "Telegram is limiting how fast this bot can send. Wait a moment and try again."
	}
	return ""
}

// ErrNoToken is returned when a helper is called without a bot token.
var ErrNoToken = errors.New("telegram: a bot token is needed. Create a bot with @BotFather and paste the token it gives you")
