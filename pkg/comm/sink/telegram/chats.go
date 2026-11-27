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
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// ChatKind is who a chat reaches, in the words the form uses.
type ChatKind string

const (
	KindPerson  ChatKind = "person"
	KindGroup   ChatKind = "group"
	KindChannel ChatKind = "channel"
)

// Bot is the bot a token belongs to.
type Bot struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
	// CanJoinGroups is false when the bot was set, in @BotFather, to refuse
	// being added to groups.
	CanJoinGroups bool `json:"can_join_groups"`
	// ReadsAllGroupMessages is false while privacy mode is on: the bot then
	// sees only commands and messages that mention or reply to it.
	ReadsAllGroupMessages bool `json:"reads_all_group_messages"`
}

// Topic is a topic of a forum group.
type Topic struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Chat is a chat the bot can post to.
type Chat struct {
	ID       string   `json:"id"`
	Kind     ChatKind `json:"kind"`
	Name     string   `json:"name"`
	Username string   `json:"username,omitempty"`
	// Topics are the forum topics the bot has seen a message in. A message
	// sent without a topic lands in General.
	Topics []Topic `json:"topics,omitempty"`
}

// Chats is what FindChats found.
type Chats struct {
	Bot   Bot    `json:"bot"`
	Chats []Chat `json:"chats"`
	// UpdatesUnavailable is set when Telegram would not hand over the bot's
	// recent updates; Notice says why.
	UpdatesUnavailable bool   `json:"updates_unavailable,omitempty"`
	Notice             string `json:"notice,omitempty"`
}

// FindChats reports the bot a token belongs to and the chats it can post to,
// newest first, read from the updates Telegram holds for the bot.
//
// Finding a chat ID was the hard part of setting Telegram up. The usual advice
// is to call getUpdates by hand and read the JSON, and what to look for differs
// for a person, a group, a supergroup and a channel. This reads the same
// updates and names each chat instead.
//
// It does not confirm updates: getUpdates with an offset marks everything
// before it as read, which would take messages away from anything else reading
// this bot. Telegram keeps unconfirmed updates for 24 hours, so a chat that was
// last active before that is not found; doing anything in it again finds it.
func FindChats(ctx context.Context, baseURL, token string) (Chats, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Chats{}, ErrNoToken
	}
	var me struct {
		ID                      int64  `json:"id"`
		FirstName               string `json:"first_name"`
		Username                string `json:"username"`
		CanJoinGroups           bool   `json:"can_join_groups"`
		CanReadAllGroupMessages bool   `json:"can_read_all_group_messages"`
	}
	if err := callAPI(ctx, baseURL, token, "getMe", nil, &me); err != nil {
		return Chats{}, err
	}
	out := Chats{
		Bot: Bot{
			ID:                    strconv.FormatInt(me.ID, 10),
			Name:                  me.FirstName,
			Username:              me.Username,
			CanJoinGroups:         me.CanJoinGroups,
			ReadsAllGroupMessages: me.CanReadAllGroupMessages,
		},
		Chats: []Chat{},
	}

	var updates []update
	err := callAPI(ctx, baseURL, token, "getUpdates", map[string]any{"limit": 100, "timeout": 0}, &updates)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
		out.UpdatesUnavailable = true
		out.Notice = conflictNotice(apiErr)
		return out, nil
	}
	if err != nil {
		return Chats{}, err
	}
	out.Chats = collectChats(updates)
	return out, nil
}

func conflictNotice(e *APIError) string {
	if strings.Contains(strings.ToLower(e.Description), "webhook") {
		return "This bot delivers its messages to a webhook (a Hermod Telegram chat source uses one), so Telegram will not list its recent chats here. Type the chat ID yourself, or use a separate bot for sending."
	}
	return "Something else is reading this bot's messages right now, so Telegram will not list its recent chats here. Type the chat ID yourself, or use a separate bot for sending."
}

// update is the part of a Bot API update that names a chat.
type update struct {
	UpdateID          int64       `json:"update_id"`
	Message           *tgMessage  `json:"message"`
	EditedMessage     *tgMessage  `json:"edited_message"`
	ChannelPost       *tgMessage  `json:"channel_post"`
	EditedChannelPost *tgMessage  `json:"edited_channel_post"`
	MyChatMember      *chatMember `json:"my_chat_member"`
	CallbackQuery     *struct {
		Message *tgMessage `json:"message"`
	} `json:"callback_query"`
}

type tgChat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type topicCreated struct {
	Name string `json:"name"`
}

type tgMessage struct {
	Chat              tgChat        `json:"chat"`
	MessageThreadID   int64         `json:"message_thread_id"`
	IsTopicMessage    bool          `json:"is_topic_message"`
	ForumTopicCreated *topicCreated `json:"forum_topic_created"`
	ReplyToMessage    *struct {
		ForumTopicCreated *topicCreated `json:"forum_topic_created"`
	} `json:"reply_to_message"`
	MigrateToChatID int64 `json:"migrate_to_chat_id"`
}

type chatMember struct {
	Chat          tgChat `json:"chat"`
	NewChatMember struct {
		Status string `json:"status"`
	} `json:"new_chat_member"`
}

// chatState is a chat as the updates so far describe it.
type chatState struct {
	chat     Chat
	lastSeen int
	gone     bool
	topics   map[string]string
	order    []string
}

// collectChats folds updates, oldest first as Telegram sends them, into the
// chats the bot can still post to, newest first.
func collectChats(updates []update) []Chat {
	states := map[int64]*chatState{}
	for i, u := range updates {
		foldUpdate(states, u, i)
	}
	return listChats(states)
}

// seenChat records that chat c was active at position at.
func seenChat(states map[int64]*chatState, c tgChat, at int) *chatState {
	st, ok := states[c.ID]
	if !ok {
		st = &chatState{topics: map[string]string{}}
		states[c.ID] = st
	}
	st.chat = chatFrom(c)
	st.lastSeen = at
	return st
}

func foldUpdate(states map[int64]*chatState, u update, at int) {
	if u.MyChatMember != nil {
		st := seenChat(states, u.MyChatMember.Chat, at)
		status := u.MyChatMember.NewChatMember.Status
		st.gone = status == "left" || status == "kicked"
	}
	for _, m := range []*tgMessage{u.Message, u.EditedMessage, u.ChannelPost, u.EditedChannelPost, callbackMessage(u)} {
		if m == nil || m.Chat.ID == 0 {
			continue
		}
		st := seenChat(states, m.Chat, at)
		if m.MigrateToChatID != 0 {
			// The group is now a supergroup with another ID; the old one is
			// refused from here on.
			st.gone = true
			continue
		}
		addTopic(st, m)
	}
}

// listChats is every chat still reachable, newest first.
func listChats(states map[int64]*chatState) []Chat {
	chats := make([]*chatState, 0, len(states))
	for _, st := range states {
		if !st.gone {
			chats = append(chats, st)
		}
	}
	slices.SortFunc(chats, func(a, b *chatState) int { return b.lastSeen - a.lastSeen })
	out := make([]Chat, 0, len(chats))
	for _, st := range chats {
		c := st.chat
		for _, id := range st.order {
			c.Topics = append(c.Topics, Topic{ID: id, Name: st.topics[id]})
		}
		out = append(out, c)
	}
	return out
}

func callbackMessage(u update) *tgMessage {
	if u.CallbackQuery == nil {
		return nil
	}
	return u.CallbackQuery.Message
}

func addTopic(st *chatState, m *tgMessage) {
	if !m.IsTopicMessage || m.MessageThreadID == 0 {
		return
	}
	id := strconv.FormatInt(m.MessageThreadID, 10)
	name := ""
	switch {
	case m.ForumTopicCreated != nil:
		name = m.ForumTopicCreated.Name
	case m.ReplyToMessage != nil && m.ReplyToMessage.ForumTopicCreated != nil:
		name = m.ReplyToMessage.ForumTopicCreated.Name
	}
	if _, ok := st.topics[id]; !ok {
		st.order = append(st.order, id)
	}
	if name != "" || st.topics[id] == "" {
		if name == "" {
			name = "Topic " + id
		}
		st.topics[id] = name
	}
}

func chatFrom(c tgChat) Chat {
	out := Chat{ID: strconv.FormatInt(c.ID, 10), Username: c.Username, Name: c.Title}
	switch c.Type {
	case "private":
		out.Kind = KindPerson
		out.Name = strings.TrimSpace(c.FirstName + " " + c.LastName)
	case "channel":
		out.Kind = KindChannel
	default:
		out.Kind = KindGroup
	}
	if out.Name == "" {
		out.Name = out.ID
	}
	return out
}
