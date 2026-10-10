package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/reply"
	slacksink "github.com/gsoultan/hermod/pkg/comm/sink/slack"
)

// Config keys of a chat source on the Slack platform.
const (
	configSlackSigningSecret = "slack_signing_secret"
	configSlackBotToken      = "slack_bot_token" //nolint:gosec // G101: a config key, not a credential.
)

const (
	// slackMaxSkew is how far a delivery's timestamp may be from now. Slack
	// recommends five minutes; older deliveries are replays.
	slackMaxSkew = 5 * time.Minute
	// slackPostTimeout bounds posting the answer back.
	slackPostTimeout = 10 * time.Second
)

// verifySlackSignature checks a delivery against the app's signing secret:
// X-Slack-Signature is "v0=" and the hex HMAC-SHA256 of "v0:<timestamp>:<body>",
// and X-Slack-Request-Timestamp must be within slackMaxSkew of now.
func verifySlackSignature(secret, timestamp, signature string, body []byte, now time.Time) bool {
	if secret == "" || timestamp == "" {
		return false
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	if skew := now.Sub(time.Unix(ts, 0)); skew > slackMaxSkew || skew < -slackMaxSkew {
		return false
	}
	digest, ok := strings.CutPrefix(signature, "v0=")
	if !ok {
		return false
	}
	sig, err := hex.DecodeString(digest)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + timestamp + ":"))
	mac.Write(body)
	return hmac.Equal(sig, mac.Sum(nil))
}

// slackEnvelope is the part of an Events API delivery the source reads.
type slackEnvelope struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
	TeamID    string `json:"team_id"`
	Event     struct {
		Type     string `json:"type"`
		Subtype  string `json:"subtype"`
		BotID    string `json:"bot_id"`
		User     string `json:"user"`
		Text     string `json:"text"`
		Channel  string `json:"channel"`
		TS       string `json:"ts"`
		ThreadTS string `json:"thread_ts"`
	} `json:"event"`
}

// handleSlack serves a chat source subscribed to Slack's Events API
// (message and app_mention events).
//
// Slack wants a delivery acknowledged within three seconds and retries one
// that is not, so the source acknowledges at once and posts the answer with
// chat.postMessage when the workflow has it. Retries are acknowledged and not
// processed: the first delivery already was, and a second would answer twice.
func (h *ChatHandler) handleSlack(w http.ResponseWriter, r *http.Request, src *storage.Source, fullPath string) {
	cfg := src.Config
	env, ok := h.readSlackEvent(w, r, cfg)
	if !ok {
		return
	}
	if env.Type == "url_verification" {
		writeJSON(w, http.StatusOK, map[string]string{"challenge": env.Challenge})
		return
	}
	in, answerable := slackMessage(env, r.Header.Get("X-Slack-Retry-Num") != "")
	if !answerable {
		count(platformSlack, "ignored")
		w.WriteHeader(http.StatusOK)
		return
	}
	token := credential(cfg, configSlackBotToken)
	if token == "" {
		count(platformSlack, "no_bot_token")
		h.warn("A Slack chat message was not answered: the chat source has no bot token", "path", fullPath)
		w.WriteHeader(http.StatusOK)
		return
	}

	msg, _ := newChatMessage(fullPath, platformSlack, in)
	pending, err := h.send(r.Context(), fullPath, msg)
	w.WriteHeader(http.StatusOK)
	if err != nil {
		count(platformSlack, "not_dispatched")
		h.warn("A Slack chat message was not dispatched", "path", fullPath, "error", err)
		return
	}
	// The request is answered now; the wait outlives it, bounded by the
	// source's response timeout.
	go h.answerOnSlack(context.WithoutCancel(r.Context()), pending, slackReply{
		token:    token,
		channel:  env.Event.Channel,
		threadTS: env.Event.ThreadTS,
		field:    replyFieldOf(cfg),
		timeout:  timeoutOf(cfg),
		path:     fullPath,
	})
}

// readSlackEvent reads a delivery and checks Slack signed it, answering the
// caller itself when it cannot.
func (h *ChatHandler) readSlackEvent(w http.ResponseWriter, r *http.Request, cfg map[string]string) (slackEnvelope, bool) {
	var env slackEnvelope
	body, ok := h.readBody(w, r, maxPlatformBody)
	if !ok {
		return env, false
	}
	if !verifySlackSignature(credential(cfg, configSlackSigningSecret), r.Header.Get("X-Slack-Request-Timestamp"),
		r.Header.Get("X-Slack-Signature"), body, time.Now()) {
		count(platformSlack, "unauthorized")
		h.JsonError(w, "Invalid Slack signature", http.StatusUnauthorized)
		return env, false
	}
	if err := json.Unmarshal(body, &env); err != nil {
		h.JsonError(w, "The body is not a Slack event", http.StatusBadRequest)
		return env, false
	}
	return env, true
}

// slackMessage is the chat message a delivery carries, and whether it is one
// to answer. A message's conversation is its channel, or its thread.
func slackMessage(env slackEnvelope, retry bool) (inbound, bool) {
	e := env.Event
	conversation := "slack:" + env.TeamID + ":" + e.Channel
	if e.ThreadTS != "" {
		conversation += ":" + e.ThreadTS
	}
	switch {
	case env.Type != "event_callback", retry,
		e.Type != "message" && e.Type != "app_mention",
		// The bot's own answers come back as message events: answering them
		// would never stop. Edits, joins and the like carry a subtype.
		e.BotID != "", e.Subtype != "",
		strings.TrimSpace(e.Text) == "", !reply.ValidConversationID(conversation):
		return inbound{}, false
	}
	return inbound{
		ConversationID: conversation,
		Message:        truncate(e.Text, maxMessageBytes),
		User:           e.User,
		Metadata: map[string]any{
			"team_id":   env.TeamID,
			"channel":   e.Channel,
			"ts":        e.TS,
			"thread_ts": e.ThreadTS,
		},
	}, true
}

// slackReply is where and how an answer is posted.
type slackReply struct {
	token, channel, threadTS, field, path string
	timeout                               time.Duration
}

// answerOnSlack waits for the workflow's answer and posts it to the channel,
// in the thread the message was in.
func (h *ChatHandler) answerOnSlack(ctx context.Context, pending *reply.Pending, to slackReply) {
	defer pending.Cancel()
	waitCtx, cancel := context.WithTimeout(ctx, to.timeout)
	defer cancel()
	outcome, err := pending.Wait(waitCtx)
	if err != nil {
		count(platformSlack, "pending")
		h.warn("A Slack chat message was not answered within the response timeout", "path", to.path, "timeout", to.timeout)
		return
	}
	count(platformSlack, string(outcome.Status))
	if !answered(outcome.Status) {
		h.warn("A Slack chat message was not answered: the workflow failed", "path", to.path, "error", outcome.Error)
		return
	}
	text := replyText(outcome.Record, to.field)
	if strings.TrimSpace(text) == "" {
		return
	}
	postCtx, cancelPost := context.WithTimeout(ctx, slackPostTimeout)
	defer cancelPost()
	sink := slacksink.NewSlackSink("", to.token, "", nil)
	sink.SetBaseURL(h.slackAPI)
	if err := sink.PostReply(postCtx, to.channel, to.threadTS, text); err != nil {
		h.warn("Posting a chat answer to Slack failed", "path", to.path, "error", err)
	}
}
