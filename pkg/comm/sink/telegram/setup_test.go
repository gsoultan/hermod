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
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

// fakeBotAPI answers the Bot API methods the setup helpers call. Each value
// is the raw JSON body the method answers with; a method missing from the map
// answers 404 the way Telegram does for an unknown method.
func fakeBotAPI(t *testing.T, answers map[string]string, seen *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if seen != nil {
			body, _ := io.ReadAll(r.Body)
			*seen = append(*seen, method+" "+string(body))
		}
		body, ok := answers[method]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":404,"description":"Not Found"}`)
			return
		}
		var probe struct {
			ErrorCode int `json:"error_code"`
		}
		_ = json.Unmarshal([]byte(body), &probe)
		if probe.ErrorCode != 0 {
			w.WriteHeader(probe.ErrorCode)
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

const getMeOK = `{"ok":true,"result":{"id":42,"is_bot":true,"first_name":"Hermod Alerts","username":"hermod_alerts_bot","can_join_groups":true,"can_read_all_group_messages":false}}`

// The chat ID was the hard part of setting Telegram up: nothing in Hermod
// said where to find it, and the usual advice — call getUpdates by hand and
// read the JSON — differs for a person, a group, a supergroup and a channel.
// FindChats reads the same updates and names each chat the bot has seen.
func TestFindChatsListsEveryKindOfChatTheBotHasSeen(t *testing.T) {
	updates := `{"ok":true,"result":[
	  {"update_id":1,"message":{"message_id":1,"chat":{"id":1001,"type":"private","first_name":"Ana","last_name":"Lim","username":"ana"},"text":"/start"}},
	  {"update_id":2,"my_chat_member":{"chat":{"id":-200,"type":"group","title":"Ops"},"new_chat_member":{"status":"member"}}},
	  {"update_id":3,"channel_post":{"message_id":5,"chat":{"id":-1003,"type":"channel","title":"Announcements","username":"acme_news"}}},
	  {"update_id":4,"message":{"message_id":9,"chat":{"id":-1004,"type":"supergroup","title":"Support","is_forum":true},"message_thread_id":7,"is_topic_message":true,"forum_topic_created":{"name":"Alerts"}}},
	  {"update_id":5,"message":{"message_id":2,"chat":{"id":1001,"type":"private","first_name":"Ana","last_name":"Lim","username":"ana"},"text":"again"}}
	]}`
	srv := fakeBotAPI(t, map[string]string{"getMe": getMeOK, "getUpdates": updates}, nil)

	got, err := FindChats(t.Context(), srv.URL, "123:abc")
	if err != nil {
		t.Fatalf("FindChats: %v", err)
	}
	if got.Bot.Username != "hermod_alerts_bot" || got.Bot.ReadsAllGroupMessages {
		t.Fatalf("bot = %+v, want hermod_alerts_bot with privacy mode on", got.Bot)
	}
	want := map[string]Chat{
		"1001":  {ID: "1001", Kind: KindPerson, Name: "Ana Lim", Username: "ana"},
		"-200":  {ID: "-200", Kind: KindGroup, Name: "Ops"},
		"-1003": {ID: "-1003", Kind: KindChannel, Name: "Announcements", Username: "acme_news"},
		"-1004": {ID: "-1004", Kind: KindGroup, Name: "Support", Topics: []Topic{{ID: "7", Name: "Alerts"}}},
	}
	if len(got.Chats) != len(want) {
		t.Fatalf("chats = %+v, want %d distinct chats (a chat seen twice is listed once)", got.Chats, len(want))
	}
	for _, c := range got.Chats {
		w, ok := want[c.ID]
		if !ok {
			t.Fatalf("unexpected chat %+v", c)
		}
		if c.Kind != w.Kind || c.Name != w.Name || c.Username != w.Username || len(c.Topics) != len(w.Topics) {
			t.Errorf("chat %s = %+v, want %+v", c.ID, c, w)
		}
		for i := range w.Topics {
			if c.Topics[i] != w.Topics[i] {
				t.Errorf("chat %s topic %d = %+v, want %+v", c.ID, i, c.Topics[i], w.Topics[i])
			}
		}
	}
	// Newest first: the chat someone just set up is the one they want.
	if got.Chats[0].ID != "1001" {
		t.Errorf("first chat = %s, want the most recently active (1001)", got.Chats[0].ID)
	}
}

// A group upgraded to a supergroup gets a new ID, and the old one is refused
// from then on. Offering the old ID would save a sink that never delivers.
func TestFindChatsReplacesAnUpgradedGroupWithItsSupergroup(t *testing.T) {
	updates := `{"ok":true,"result":[
	  {"update_id":1,"message":{"message_id":1,"chat":{"id":-300,"type":"group","title":"Ops"},"text":"hi"}},
	  {"update_id":2,"message":{"message_id":2,"chat":{"id":-300,"type":"group","title":"Ops"},"migrate_to_chat_id":-1000000000300}},
	  {"update_id":3,"message":{"message_id":1,"chat":{"id":-1000000000300,"type":"supergroup","title":"Ops"},"migrate_from_chat_id":-300}}
	]}`
	srv := fakeBotAPI(t, map[string]string{"getMe": getMeOK, "getUpdates": updates}, nil)

	got, err := FindChats(t.Context(), srv.URL, "123:abc")
	if err != nil {
		t.Fatalf("FindChats: %v", err)
	}
	if len(got.Chats) != 1 || got.Chats[0].ID != "-1000000000300" {
		t.Fatalf("chats = %+v, want only the supergroup -1000000000300", got.Chats)
	}
}

// A bot removed from a group cannot post there, so the group is not offered.
func TestFindChatsLeavesOutChatsTheBotWasRemovedFrom(t *testing.T) {
	updates := `{"ok":true,"result":[
	  {"update_id":1,"my_chat_member":{"chat":{"id":-200,"type":"group","title":"Ops"},"new_chat_member":{"status":"member"}}},
	  {"update_id":2,"my_chat_member":{"chat":{"id":-200,"type":"group","title":"Ops"},"new_chat_member":{"status":"left"}}},
	  {"update_id":3,"my_chat_member":{"chat":{"id":1001,"type":"private","first_name":"Ana"},"new_chat_member":{"status":"kicked"}}}
	]}`
	srv := fakeBotAPI(t, map[string]string{"getMe": getMeOK, "getUpdates": updates}, nil)

	got, err := FindChats(t.Context(), srv.URL, "123:abc")
	if err != nil {
		t.Fatalf("FindChats: %v", err)
	}
	if len(got.Chats) != 0 {
		t.Fatalf("chats = %+v, want none: the bot left the group and the person blocked it", got.Chats)
	}
}

// A bot whose updates go to a webhook — a Hermod Telegram chat source, for
// one — cannot be asked for them. That is not a broken token: the bot is
// reported, and the reason the list is empty is said rather than shown as an
// error.
func TestFindChatsExplainsAWebhookInsteadOfFailing(t *testing.T) {
	conflict := `{"ok":false,"error_code":409,"description":"Conflict: can't use getUpdates method while webhook is active; use deleteWebhook to delete the webhook first"}`
	srv := fakeBotAPI(t, map[string]string{"getMe": getMeOK, "getUpdates": conflict}, nil)

	got, err := FindChats(t.Context(), srv.URL, "123:abc")
	if err != nil {
		t.Fatalf("FindChats: %v", err)
	}
	if !got.UpdatesUnavailable || got.Notice == "" {
		t.Fatalf("got %+v, want UpdatesUnavailable with a notice", got)
	}
	if got.Bot.Username != "hermod_alerts_bot" {
		t.Errorf("bot = %+v, want it reported even without updates", got.Bot)
	}
}

// FindChats must not consume updates: a getUpdates call with an offset
// confirms everything before it, which would take messages away from anything
// else reading this bot.
func TestFindChatsDoesNotConfirmUpdates(t *testing.T) {
	var seen []string
	srv := fakeBotAPI(t, map[string]string{"getMe": getMeOK, "getUpdates": `{"ok":true,"result":[]}`}, &seen)
	if _, err := FindChats(t.Context(), srv.URL, "123:abc"); err != nil {
		t.Fatalf("FindChats: %v", err)
	}
	for _, call := range seen {
		if strings.HasPrefix(call, "getUpdates") && strings.Contains(call, "offset") {
			t.Fatalf("getUpdates was called with an offset: %s", call)
		}
	}
}

func TestFindChatsRefusesABadToken(t *testing.T) {
	srv := fakeBotAPI(t, map[string]string{"getMe": `{"ok":false,"error_code":401,"description":"Unauthorized"}`}, nil)
	_, err := FindChats(t.Context(), srv.URL, "123:wrong")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Hint == "" {
		t.Fatalf("err = %v, want an *APIError with a hint", err)
	}
	if strings.Contains(err.Error(), "123:wrong") {
		t.Fatalf("error %q quotes the token", err)
	}
}

func TestFindChatsNeedsAToken(t *testing.T) {
	if _, err := FindChats(t.Context(), "http://unused", "  "); err == nil {
		t.Fatal("FindChats with no token succeeded")
	}
}

// Telegram's refusals are terse ("chat not found"), and each has one fix that
// depends on the kind of chat. The hint is that fix.
func TestAPIErrorsCarryTheFix(t *testing.T) {
	cases := []struct {
		name, body, wantHint string
		wantMigrate          string
	}{
		{"person never started the bot", `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`, "Start", ""},
		{"person blocked the bot", `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`, "unblock", ""},
		{"bot cannot message first", `{"ok":false,"error_code":403,"description":"Forbidden: bot can't initiate conversation with a user"}`, "Start", ""},
		{"not in the channel", `{"ok":false,"error_code":403,"description":"Forbidden: bot is not a member of the channel chat"}`, "administrator", ""},
		{"kicked from group", `{"ok":false,"error_code":403,"description":"Forbidden: bot was kicked from the group chat"}`, "Add the bot", ""},
		{"no rights", `{"ok":false,"error_code":400,"description":"Bad Request: not enough rights to send text messages to the chat"}`, "permission", ""},
		{"topic gone", `{"ok":false,"error_code":400,"description":"Bad Request: message thread not found"}`, "topic", ""},
		{"upgraded", `{"ok":false,"error_code":400,"description":"Bad Request: group chat was upgraded to a supergroup chat","parameters":{"migrate_to_chat_id":-1001234}}`, "-1001234", "-1001234"},
		{"bad token", `{"ok":false,"error_code":401,"description":"Unauthorized"}`, "BotFather", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := fakeBotAPI(t, map[string]string{"sendMessage": tc.body}, nil)
			_, err := SendTestMessage(t.Context(), srv.URL, "123:abc", "1", "", "hi")
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v, want *APIError", err)
			}
			if !strings.Contains(apiErr.Hint, tc.wantHint) {
				t.Errorf("hint = %q, want it to mention %q", apiErr.Hint, tc.wantHint)
			}
			if apiErr.MigrateToChatID != tc.wantMigrate {
				t.Errorf("MigrateToChatID = %q, want %q", apiErr.MigrateToChatID, tc.wantMigrate)
			}
		})
	}
}

func TestSendTestMessagePostsToTheTopicAndNamesTheChat(t *testing.T) {
	var seen []string
	srv := fakeBotAPI(t, map[string]string{
		"sendMessage": `{"ok":true,"result":{"message_id":3,"chat":{"id":-1004,"type":"supergroup","title":"Support"}}}`,
	}, &seen)

	sent, err := SendTestMessage(t.Context(), srv.URL, "123:abc", "-1004", "7", "hello")
	if err != nil {
		t.Fatalf("SendTestMessage: %v", err)
	}
	if sent.Name != "Support" || sent.Kind != KindGroup {
		t.Errorf("sent = %+v, want the Support group", sent)
	}
	if len(seen) != 1 || !strings.Contains(seen[0], `"message_thread_id":7`) || !strings.Contains(seen[0], `"chat_id":"-1004"`) {
		t.Fatalf("request = %v, want chat -1004 and topic 7", seen)
	}
}

func TestSendTestMessageRefusesATopicThatIsNotANumber(t *testing.T) {
	if _, err := SendTestMessage(t.Context(), "http://unused", "123:abc", "-1004", "alerts", "hi"); err == nil {
		t.Fatal("a topic of \"alerts\" was accepted")
	}
}

// A forum group posts to its General topic unless a message names another.
// The sink had no way to name one, so a group with topics could only ever
// receive alerts in General.
func TestSinkPostsIntoTheConfiguredTopic(t *testing.T) {
	var seen []string
	srv := fakeBotAPI(t, map[string]string{"sendMessage": `{"ok":true,"result":{"message_id":1,"chat":{"id":-1004,"type":"supergroup"}}}`}, &seen)
	s := NewTelegramSink("123:abc", "-1004", nil)
	s.SetBaseURL(srv.URL)
	if err := s.SetMessageThreadID("7"); err != nil {
		t.Fatalf("SetMessageThreadID: %v", err)
	}
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetPayload([]byte("hello"))
	if err := s.Write(t.Context(), msg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(seen) != 1 || !strings.Contains(seen[0], `"message_thread_id":7`) {
		t.Fatalf("request = %v, want message_thread_id 7", seen)
	}
}

func TestSinkWithoutATopicSendsNone(t *testing.T) {
	var seen []string
	srv := fakeBotAPI(t, map[string]string{"sendMessage": `{"ok":true,"result":{"message_id":1,"chat":{"id":5,"type":"private"}}}`}, &seen)
	s := NewTelegramSink("123:abc", "5", nil)
	s.SetBaseURL(srv.URL)
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetPayload([]byte("hello"))
	if err := s.Write(t.Context(), msg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if strings.Contains(seen[0], "message_thread_id") {
		t.Fatalf("request %s names a topic nobody configured", seen[0])
	}
}

// A run that fails says how to fix it, not only Telegram's two words.
func TestSinkErrorsCarryTheHint(t *testing.T) {
	srv := fakeBotAPI(t, map[string]string{"sendMessage": `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`}, nil)
	s := NewTelegramSink("123:abc", "5", nil)
	s.SetBaseURL(srv.URL)
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetPayload([]byte("hello"))
	err := s.Write(t.Context(), msg)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Hint == "" {
		t.Fatalf("err = %v, want *APIError with a hint", err)
	}
}
