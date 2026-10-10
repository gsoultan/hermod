package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// A chat source on the Slack platform takes the Events API's deliveries,
// checks Slack signed them, and answers in the channel (and thread) the
// message was posted in, with the bot token the source holds.

const slackSecret = "8f742231b10e8888abcd99yyyzzz85a5"

func slackSigned(body string, at time.Time) http.Header {
	ts := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(slackSecret))
	mac.Write([]byte("v0:" + ts + ":" + body))
	return http.Header{
		"X-Slack-Request-Timestamp": {ts},
		"X-Slack-Signature":         {"v0=" + hex.EncodeToString(mac.Sum(nil))},
		"Content-Type":              {"application/json"},
	}
}

func slackConfig() map[string]string {
	return map[string]string{
		"platform":             "slack",
		"slack_signing_secret": slackSecret,
		"slack_bot_token":      "xoxb-test",
	}
}

func TestVerifySlackSignature(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{"type":"event_callback"}`)
	good := slackSigned(string(body), now)
	tests := []struct {
		name      string
		secret    string
		timestamp string
		signature string
		want      bool
	}{
		{"signed now", slackSecret, good.Get("X-Slack-Request-Timestamp"), good.Get("X-Slack-Signature"), true},
		{"another secret", "other", good.Get("X-Slack-Request-Timestamp"), good.Get("X-Slack-Signature"), false},
		{"no secret", "", good.Get("X-Slack-Request-Timestamp"), good.Get("X-Slack-Signature"), false},
		{"no signature", slackSecret, good.Get("X-Slack-Request-Timestamp"), "", false},
		{"a signature without its version", slackSecret, good.Get("X-Slack-Request-Timestamp"), good.Get("X-Slack-Signature")[3:], false},
		{"not hex", slackSecret, good.Get("X-Slack-Request-Timestamp"), "v0=zz", false},
		{"another timestamp", slackSecret, strconv.FormatInt(now.Unix()+1, 10), good.Get("X-Slack-Signature"), false},
		{"no timestamp", slackSecret, "", good.Get("X-Slack-Signature"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := verifySlackSignature(tt.secret, tt.timestamp, tt.signature, body, now); got != tt.want {
				t.Errorf("verifySlackSignature = %v, want %v", got, tt.want)
			}
		})
	}

	// A replayed delivery: correctly signed, but long ago.
	old := now.Add(-10 * time.Minute)
	stale := slackSigned(string(body), old)
	if verifySlackSignature(slackSecret, stale.Get("X-Slack-Request-Timestamp"), stale.Get("X-Slack-Signature"), body, now) {
		t.Error("a delivery signed ten minutes ago was accepted")
	}
}

func TestASlackChatAnswersTheURLVerification(t *testing.T) {
	c := newChat(t, "slack-verify", slackConfig(), chatOptions{noEngine: true})
	body := `{"type":"url_verification","challenge":"3eZbrw1aBm2rZgRNFdxV2595E9CY3gmdALWMmHkvFXO7tYXAYM8P"}`
	rec := c.post(t, "/api/chat/slack-verify", body, slackSigned(body, time.Now()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Challenge string `json:"challenge"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Challenge != "3eZbrw1aBm2rZgRNFdxV2595E9CY3gmdALWMmHkvFXO7tYXAYM8P" {
		t.Errorf("answered %s", rec.Body.String())
	}
}

func TestASlackChatRefusesAnUnsignedDelivery(t *testing.T) {
	body := `{"type":"url_verification","challenge":"x"}`
	noSecret := slackConfig()
	delete(noSecret, "slack_signing_secret")
	tests := []struct {
		name   string
		config map[string]string
		header http.Header
	}{
		{"unsigned", slackConfig(), nil},
		{"signed with another body", slackConfig(), slackSigned(`{"type":"other"}`, time.Now())},
		{"replayed", slackConfig(), slackSigned(body, time.Now().Add(-time.Hour))},
		{"no signing secret configured", noSecret, slackSigned(body, time.Now())},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := "slack-unsigned-" + strconv.Itoa(i)
			c := newChat(t, path, tt.config, chatOptions{noEngine: true})
			if rec := c.post(t, "/api/chat/"+path, body, tt.header); rec.Code != http.StatusUnauthorized {
				t.Errorf("status %d, want 401: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// slackAPI is a stand-in for Slack's Web API that records what was posted.
func slackAPI(t *testing.T) (url string, posted <-chan map[string]string) {
	t.Helper()
	ch := make(chan map[string]string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]string
		_ = json.NewDecoder(r.Body).Decode(&got)
		got["authorization"] = r.Header.Get("Authorization")
		got["path"] = r.URL.Path
		ch <- got
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, ch
}

func TestASlackChatAnswersInTheThreadItWasAskedIn(t *testing.T) {
	c := newChat(t, "slack-reply", slackConfig(), chatOptions{})
	api, posted := slackAPI(t)
	c.h.slackAPI = api

	body := `{"type":"event_callback","team_id":"T1","event":{"type":"app_mention","user":"U7","text":"what is the status?",` +
		`"channel":"C42","ts":"1700000000.000200","thread_ts":"1700000000.000100","channel_type":"channel"}}`
	rec := c.post(t, "/api/chat/slack-reply", body, slackSigned(body, time.Now()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}

	select {
	case got := <-posted:
		if got["path"] != "/chat.postMessage" || got["authorization"] != "Bearer xoxb-test" {
			t.Errorf("posted to %s with %q", got["path"], got["authorization"])
		}
		if got["channel"] != "C42" || got["thread_ts"] != "1700000000.000100" || got["text"] != "echo: what is the status?" {
			t.Errorf("posted %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was posted to Slack")
	}

	data, meta := c.workflow.last(t)
	if data["conversation_id"] != "slack:T1:C42:1700000000.000100" || data["user"] != "U7" {
		t.Errorf("the workflow saw %v", data)
	}
	if meta["chat_platform"] != "slack" {
		t.Errorf("metadata %v", meta)
	}
}

// What the source does not answer: the bot's own messages, which would loop,
// edits and other subtypes, and Slack's retries of a delivery already
// acknowledged, which would answer twice.
func TestASlackChatIgnoresWhatItShouldNotAnswer(t *testing.T) {
	event := func(extra string) string {
		return `{"type":"event_callback","team_id":"T1","event":{"type":"message","user":"U7","text":"hi","channel":"D1","ts":"1.2"` + extra + `}}`
	}
	tests := []struct {
		name   string
		body   string
		header http.Header
	}{
		{"a bot's message", event(`,"bot_id":"B1"`), nil},
		{"an edit", event(`,"subtype":"message_changed"`), nil},
		{"an empty message", `{"type":"event_callback","team_id":"T1","event":{"type":"message","user":"U7","text":"  ","channel":"D1","ts":"1.2"}}`, nil},
		{"a retry", event(""), http.Header{"X-Slack-Retry-Num": {"1"}}},
		{"another event", `{"type":"event_callback","team_id":"T1","event":{"type":"reaction_added","user":"U7"}}`, nil},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := "slack-ignore-" + strconv.Itoa(i)
			c := newChat(t, path, slackConfig(), chatOptions{noEngine: true})
			header := slackSigned(tt.body, time.Now())
			for k, v := range tt.header {
				header[k] = v
			}
			rec := c.post(t, "/api/chat/"+path, tt.body, header)
			if rec.Code != http.StatusOK {
				t.Errorf("status %d, want 200 so Slack does not retry: %s", rec.Code, rec.Body.String())
			}
			c.assertNothingDispatched(t)
		})
	}
}

// assertNothingDispatched reads the source's path: the handler dispatches
// before it answers, so a message that is not there by now never will be.
func (c *chat) assertNothingDispatched(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if msg, err := c.src.Read(ctx); err == nil {
		t.Errorf("a message was dispatched: %v", msg.Data())
	}
}
