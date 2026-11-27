// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

// fakeTelegram stands in for the Bot API and records the paths it was asked
// for, which carry the token.
type fakeTelegram struct {
	mu      sync.Mutex
	paths   []string
	bodies  []string
	answers map[string]string
}

func (f *fakeTelegram) start(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		f.bodies = append(f.bodies, string(body))
		f.mu.Unlock()
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		answer, ok := f.answers[method]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":404,"description":"Not Found"}`)
			return
		}
		var probe struct {
			ErrorCode int `json:"error_code"`
		}
		_ = json.Unmarshal([]byte(answer), &probe)
		if probe.ErrorCode != 0 {
			w.WriteHeader(probe.ErrorCode)
		}
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	prev := telegramAPIBase
	telegramAPIBase = srv.URL
	t.Cleanup(func() { telegramAPIBase = prev })
}

func telegramMux(t *testing.T) *http.ServeMux {
	t.Helper()
	t.Cleanup(func() { evaluator.SetSecretSource(nil) })
	store := vhostSecretStorage{secrets: map[string]string{"sales/TG_TOKEN": "123:resolved"}}
	mux := http.NewServeMux()
	(&SinkHandler{Handler: &handlers.Handler{Storage: store, Registry: registry.NewRegistry(store)}}).RegisterSinkRoutes(mux)
	return mux
}

func postAs(t *testing.T, mux *http.ServeMux, user *storage.User, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, user))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

var salesEditor = &storage.User{Role: storage.RoleEditor, VHosts: []string{"sales"}}

// The chat list is read with the token a run would use: a secret reference in
// the form is resolved in the sink's vhost before Telegram is asked.
func TestFindTelegramChatsResolvesTheVHostSecret(t *testing.T) {
	tg := &fakeTelegram{answers: map[string]string{
		"getMe":      `{"ok":true,"result":{"id":1,"first_name":"Alerts","username":"alerts_bot","can_join_groups":true}}`,
		"getUpdates": `{"ok":true,"result":[{"update_id":1,"message":{"message_id":1,"chat":{"id":-200,"type":"group","title":"Ops"}}}]}`,
	}}
	tg.start(t)
	mux := telegramMux(t)

	rec := postAs(t, mux, salesEditor, "/api/sinks/telegram/chats",
		`{"vhost":"sales","config":{"bot_token":"{{secret(\"TG_TOKEN\")}}"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Bot struct {
			Username string `json:"username"`
		} `json:"bot"`
		Chats []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"chats"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	if out.Bot.Username != "alerts_bot" || len(out.Chats) != 1 || out.Chats[0].ID != "-200" || out.Chats[0].Kind != "group" {
		t.Fatalf("answer = %+v", out)
	}
	if len(tg.paths) == 0 || !strings.Contains(tg.paths[0], "/bot123:resolved/") {
		t.Fatalf("telegram was asked %v, want the resolved token", tg.paths)
	}
}

// Naming a vhost is asking for its secrets, so a caller outside it is
// refused before anything is resolved or sent.
func TestTelegramSetupRefusesACallerOutsideTheVHost(t *testing.T) {
	tg := &fakeTelegram{answers: map[string]string{}}
	tg.start(t)
	mux := telegramMux(t)
	other := &storage.User{Role: storage.RoleEditor, VHosts: []string{"other"}}
	for _, path := range []string{"/api/sinks/telegram/chats", "/api/sinks/telegram/test-send"} {
		rec := postAs(t, mux, other, path, `{"vhost":"sales","config":{"bot_token":"{{secret(\"TG_TOKEN\")}}","chat_id":"1"}}`)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s answered %d, want 403", path, rec.Code)
		}
	}
	if len(tg.paths) != 0 {
		t.Errorf("telegram was called %v for a refused caller", tg.paths)
	}
}

func TestSendTelegramTestPostsToTheChatAndTopic(t *testing.T) {
	tg := &fakeTelegram{answers: map[string]string{
		"sendMessage": `{"ok":true,"result":{"message_id":9,"chat":{"id":-1004,"type":"supergroup","title":"Support"}}}`,
	}}
	tg.start(t)
	mux := telegramMux(t)

	rec := postAs(t, mux, salesEditor, "/api/sinks/telegram/test-send",
		`{"vhost":"sales","config":{"bot_token":"1:a","chat_id":"-1004","message_thread_id":"7"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"Support"`) {
		t.Errorf("answer %s does not name the chat", rec.Body.String())
	}
	if len(tg.bodies) != 1 || !strings.Contains(tg.bodies[0], `"message_thread_id":7`) {
		t.Errorf("sent %v, want topic 7", tg.bodies)
	}
}

// A refusal comes back with the fix and, for an upgraded group, the new ID,
// so the form can offer to use it.
func TestSendTelegramTestAnswersWithTheFix(t *testing.T) {
	tg := &fakeTelegram{answers: map[string]string{
		"sendMessage": `{"ok":false,"error_code":400,"description":"Bad Request: group chat was upgraded to a supergroup chat","parameters":{"migrate_to_chat_id":-1009}}`,
	}}
	tg.start(t)
	mux := telegramMux(t)

	rec := postAs(t, mux, salesEditor, "/api/sinks/telegram/test-send",
		`{"vhost":"sales","config":{"bot_token":"1:secretpart","chat_id":"-200"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["migrate_to_chat_id"] != "-1009" || out["hint"] == "" || out["error"] == "" {
		t.Errorf("answer = %v, want error, hint and the new chat ID", out)
	}
	if strings.Contains(rec.Body.String(), "secretpart") {
		t.Errorf("answer %s quotes the token", rec.Body.String())
	}
}
