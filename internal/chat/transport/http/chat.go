package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/reply"
	"github.com/gsoultan/hermod/pkg/comm/source/webhook"
	"github.com/gsoultan/hermod/pkg/comm/transformer/genai"
	"github.com/gsoultan/hermod/pkg/comm/transformer/genai/memory"
	"github.com/prometheus/client_golang/prometheus"
)

// SourceType is the source type a chat source is stored under.
const SourceType = "chat"

// Config keys a chat source reads. response_timeout is reply.ConfigTimeout.
const (
	configPlatform       = "platform"
	configAPIKey         = "api_key"
	configWidgetKey      = "widget_key"
	configAllowedOrigins = "allowed_origins"
	configReplyField     = "reply_field"
	configRateLimit      = "rate_limit"
)

// Platforms a chat source receives from.
const (
	platformWeb      = "web"
	platformSlack    = "slack"
	platformTelegram = "telegram"
)

const (
	// maxWebBody bounds a web request: a message, a user and some metadata.
	maxWebBody = 16 << 10
	// maxPlatformBody bounds a Slack or Telegram delivery, which carries much
	// more around the text than a web request does.
	maxPlatformBody = 256 << 10
	// maxMessageBytes is what an AI Prompt node's memory keeps of one turn. A
	// longer message from the web is refused; one from Slack or Telegram,
	// whose sender cannot be told, is cut.
	maxMessageBytes = memory.MaxTurnBytes
	maxUserBytes    = 256
	// defaultRateLimit is messages per caller per hour when the source does
	// not say.
	defaultRateLimit = 120

	headerAPIKey   = "X-API-Key"  //nolint:gosec // G101: a header name, not a credential.
	queryWidgetKey = "widget_key" //nolint:gosec // G101: a query parameter name, not a credential.

	msgCannotAuthenticate = "the chat's credentials cannot be checked right now"
	// msgWorkflowFailed is all a caller learns of a failure. The cause is in
	// the run history, and may name a provider, a model or a key.
	msgWorkflowFailed = "the workflow could not answer this message"
)

var chatMessages = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "hermod_chat_messages_total",
	Help: "Chat messages received, by platform and what became of them.",
}, []string{"platform", "outcome"})

func init() {
	// Best-effort register; ignore duplicate registrations when hot reloading in dev
	_ = prometheus.Register(chatMessages)
}

// inbound is one chat message, whichever platform it came from. It is the
// record the workflow receives.
type inbound struct {
	ConversationID string         `json:"conversation_id"`
	Message        string         `json:"message"`
	User           string         `json:"user,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

// chatResponse is what a web caller is answered with.
type chatResponse struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	ConversationID string `json:"conversation_id"`
	Reply          string `json:"reply,omitempty"`
	Error          string `json:"error,omitempty"`
}

// HandleChat receives one message for the chat source on the request's path
// and answers with the workflow's reply.
func (h *ChatHandler) HandleChat(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if path == "" {
		h.JsonError(w, "Path is required", http.StatusBadRequest)
		return
	}
	fullPath := "/api/chat/" + path

	src, err := h.chatSource(r.Context(), fullPath)
	if err != nil {
		h.JsonError(w, msgCannotAuthenticate, http.StatusServiceUnavailable)
		return
	}
	if src == nil {
		h.JsonError(w, "Chat not found", http.StatusNotFound)
		return
	}
	switch src.Config[configPlatform] {
	case "", platformWeb:
		h.handleWeb(w, r, src, fullPath)
	case platformSlack:
		h.handleSlack(w, r, src, fullPath)
	case platformTelegram:
		h.handleTelegram(w, r, src, fullPath)
	default:
		h.JsonError(w, "this chat source names a platform Hermod does not know", http.StatusInternalServerError)
	}
}

// chatSource returns the chat source that holds fullPath, or nil when none
// does. An error means the store could not be read: the source's credentials
// are in its configuration, so the caller must refuse the request.
func (h *ChatHandler) chatSource(ctx context.Context, fullPath string) (*storage.Source, error) {
	sources, _, err := h.Storage.ListSources(ctx, storage.CommonFilter{})
	if err != nil {
		return nil, err
	}
	for _, src := range sources {
		if src.Type == SourceType && src.Config["path"] == fullPath {
			// Credentials may be secret references, resolved the way the
			// registry resolves a running source's config.
			if h.Registry != nil {
				src.Config = h.Registry.ResolveSecrets(ctx, src.VHost, src.Config)
			}
			return &src, nil
		}
	}
	return nil, nil
}

// handleWeb serves the generic web endpoint: a JSON request, answered
// synchronously.
func (h *ChatHandler) handleWeb(w http.ResponseWriter, r *http.Request, src *storage.Source, fullPath string) {
	cfg := src.Config
	if !h.admitWeb(w, r, src) {
		return
	}
	body, ok := h.readBody(w, r, maxWebBody)
	if !ok {
		return
	}
	in, problem := parseWebRequest(body)
	if problem != "" {
		h.JsonError(w, problem, http.StatusBadRequest)
		return
	}

	msg, id := newChatMessage(fullPath, platformWeb, in)
	pending, err := h.send(r.Context(), fullPath, msg)
	if err != nil {
		count(platformWeb, "not_dispatched")
		h.JsonError(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer pending.Cancel()

	ctx, cancel := context.WithTimeout(r.Context(), timeoutOf(cfg))
	defer cancel()
	outcome, err := pending.Wait(ctx)
	if err != nil {
		// The wait ran out, or the caller went away. The message is still the
		// workflow's and may yet be answered, so this is not a failure: a
		// caller that retries a failure sends the message twice.
		count(platformWeb, "pending")
		writeJSON(w, http.StatusAccepted, chatResponse{ID: id, Status: "pending", ConversationID: in.ConversationID})
		return
	}
	count(platformWeb, string(outcome.Status))
	res := chatResponse{ID: id, Status: string(outcome.Status), ConversationID: in.ConversationID}
	if !answered(outcome.Status) {
		res.Error = msgWorkflowFailed
		writeJSON(w, http.StatusBadGateway, res)
		return
	}
	res.Reply = replyText(outcome.Record, replyFieldOf(cfg))
	writeJSON(w, http.StatusOK, res)
}

// admitWeb applies the web endpoint's rate limit and credentials, answering
// the caller itself when the request is refused.
func (h *ChatHandler) admitWeb(w http.ResponseWriter, r *http.Request, src *storage.Source) bool {
	cfg := src.Config
	origin := r.Header.Get("Origin")
	// Set before anything can fail, so the widget can read why it did. No
	// credentials: the widget sends none, and a page on an allowed origin has
	// no business carrying the visitor's Hermod session here.
	if origin != "" && originAllowed(origin, cfg[configAllowedOrigins]) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Del("Access-Control-Allow-Credentials")
	}
	if h.IsRateLimited(r, "chat:"+src.ID, rateLimitOf(cfg)) {
		count(platformWeb, "rate_limited")
		h.JsonError(w, "Too many messages", http.StatusTooManyRequests)
		return false
	}
	if status, msg := authenticateWeb(r, cfg, origin); msg != "" {
		count(platformWeb, "unauthorized")
		h.JsonError(w, msg, status)
		return false
	}
	return true
}

// authenticateWeb holds a web request to the source's credentials: its API
// key, for a server calling it, or its widget key, which is public and so is
// accepted only from a page on one of the source's allowed origins. A source
// with neither answers nobody. It returns an HTTP status and a client-facing
// message; an empty message means the request is authenticated.
func authenticateWeb(r *http.Request, cfg map[string]string, origin string) (int, string) {
	if key := r.Header.Get(headerAPIKey); key != "" {
		if want := credential(cfg, configAPIKey); want == "" || !handlers.ConstantTimeCompare(key, want) {
			return http.StatusUnauthorized, "Invalid API key"
		}
		return http.StatusOK, ""
	}
	if key := r.URL.Query().Get(queryWidgetKey); key != "" {
		if want := credential(cfg, configWidgetKey); want == "" || !handlers.ConstantTimeCompare(key, want) {
			return http.StatusUnauthorized, "Invalid widget key"
		}
		if origin == "" || !originAllowed(origin, cfg[configAllowedOrigins]) {
			return http.StatusForbidden, "This site may not use this chat"
		}
		return http.StatusOK, ""
	}
	return http.StatusUnauthorized, "Send the chat's API key as " + headerAPIKey + ", or its widget key from an allowed origin"
}

// credential is the credential stored under key. A secret reference still in
// the config is one whose secret does not exist, and reads as no credential:
// otherwise the reference's own text would be accepted as the key.
func credential(cfg map[string]string, key string) string {
	v := strings.TrimSpace(cfg[key])
	if strings.HasPrefix(v, "secret:") || strings.HasPrefix(v, "{{") {
		return ""
	}
	return v
}

// originAllowed reports whether origin is on the comma-separated allow-list,
// compared exactly but for case and a trailing slash. "*" matches nothing: a
// public key usable from any page is no control at all.
func originAllowed(origin, allowed string) bool {
	for a := range strings.SplitSeq(allowed, ",") {
		a = strings.TrimSuffix(strings.TrimSpace(a), "/")
		if a != "" && a != "*" && strings.EqualFold(a, origin) {
			return true
		}
	}
	return false
}

// parseWebRequest reads {conversation_id, message, user?, metadata?}. A
// missing conversation id starts a conversation. problem is the client-facing
// reason the request is refused, or empty.
func parseWebRequest(body []byte) (in inbound, problem string) {
	var req struct {
		ConversationID string          `json:"conversation_id"`
		Message        string          `json:"message"`
		User           string          `json:"user"`
		Metadata       json.RawMessage `json:"metadata"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return inbound{}, "The body must be a JSON object: {\"conversation_id\", \"message\", \"user\", \"metadata\"}"
	}
	switch {
	case strings.TrimSpace(req.Message) == "":
		return inbound{}, "message is required"
	case len(req.Message) > maxMessageBytes:
		return inbound{}, "message is longer than " + strconv.Itoa(maxMessageBytes) + " bytes"
	case len(req.User) > maxUserBytes:
		return inbound{}, "user is longer than " + strconv.Itoa(maxUserBytes) + " bytes"
	case req.ConversationID != "" && !reply.ValidConversationID(req.ConversationID):
		return inbound{}, "Invalid conversation_id: use 1 to 128 letters, digits, '.', '_', ':' or '-'"
	}
	in = inbound{ConversationID: req.ConversationID, Message: req.Message, User: req.User}
	if len(req.Metadata) > 0 && string(req.Metadata) != "null" {
		if err := json.Unmarshal(req.Metadata, &in.Metadata); err != nil {
			return inbound{}, "metadata must be a JSON object"
		}
	}
	if in.ConversationID == "" {
		in.ConversationID = reply.NewConversationID()
	}
	return in, ""
}

// readBody reads at most limit bytes of the request body, answering the
// caller itself when it cannot.
func (h *ChatHandler) readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err == nil {
		return body, true
	}
	if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
		h.JsonError(w, "The request is larger than "+strconv.FormatInt(limit, 10)+" bytes", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	h.JsonError(w, "Failed to read the request", http.StatusBadRequest)
	return nil, false
}

// newChatMessage makes the record a workflow receives for one chat message.
// The conversation id is in the record and in the metadata, where an AI
// Prompt node's memory looks for it (package memory). It returns the message's
// id, which is not to be read off the message once it has been dispatched.
func newChatMessage(fullPath, platform string, in inbound) (hermod.Message, string) {
	id := uuid.NewString()
	body, _ := json.Marshal(in)
	msg := message.AcquireMessage()
	msg.SetID(id)
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable(SourceType)
	msg.SetAfter(body)
	msg.SetMetadata("chat_path", fullPath)
	msg.SetMetadata("chat_platform", platform)
	msg.SetMetadata(reply.MetaConversationID, in.ConversationID)
	return msg, id
}

// errNotRunning is what a caller is told when no workflow receives the chat.
var errNotRunning = errors.New("the chat's workflow is not running")

// send registers a waiter for msg and dispatches it to the source holding
// fullPath, waking its workflow when it is parked. The waiter is registered
// first, or a fast workflow could finish before anyone waits. On success the
// caller cancels the waiter; on failure msg has been released.
func (h *ChatHandler) send(ctx context.Context, fullPath string, msg hermod.Message) (*reply.Pending, error) {
	pending, err := reply.Expect(msg)
	if err != nil {
		message.ReleaseMessage(msg)
		return nil, err
	}
	// A chat source receives through the webhook path registry; its paths are
	// under /api/chat/, so it never shares one with a webhook.
	if err := webhook.Dispatch(fullPath, msg); err != nil {
		if !h.WakeUpWorkflow(ctx, SourceType, fullPath) || webhook.Dispatch(fullPath, msg) != nil {
			pending.Cancel()
			message.ReleaseMessage(msg)
			return nil, errNotRunning
		}
	}
	return pending, nil
}

// answered reports whether the workflow finished with the message, as opposed
// to failing on it.
func answered(s reply.Status) bool {
	return s == reply.Delivered || s == reply.Completed || s == reply.Filtered
}

// replyFieldOf is the record field the answer is read from: by default where
// an AI Prompt node writes its answer.
func replyFieldOf(cfg map[string]string) string {
	if f := strings.TrimSpace(cfg[configReplyField]); f != "" {
		return f
	}
	return genai.DefaultPromptField
}

// replyText reads the answer out of the record the workflow left, a dotted
// path into its row (the after-image) or, failing that, its top level. A
// field that is not text is answered as JSON.
func replyText(record []byte, field string) string {
	var rec map[string]any
	if json.Unmarshal(record, &rec) != nil {
		return ""
	}
	if after, ok := rec["after"].(map[string]any); ok {
		if v, found := lookup(after, field); found {
			return asText(v)
		}
	}
	v, _ := lookup(rec, field)
	return asText(v)
}

func lookup(m map[string]any, path string) (any, bool) {
	var cur any = m
	for part := range strings.SplitSeq(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = obj[part]; !ok {
			return nil, false
		}
	}
	return cur, true
}

func asText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func rateLimitOf(cfg map[string]string) int {
	if n, err := strconv.Atoi(cfg[configRateLimit]); err == nil && n > 0 {
		return n
	}
	return defaultRateLimit
}

// timeoutOf is how long a caller waits for the answer: the source's
// response_timeout, bounded the way a synchronous webhook's is.
func timeoutOf(cfg map[string]string) time.Duration {
	_, timeout := reply.ModeOf(map[string]string{reply.ConfigMode: reply.ModeSync, reply.ConfigTimeout: cfg[reply.ConfigTimeout]})
	return timeout
}

// truncate cuts s to at most n bytes, on a rune boundary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func count(platform, outcome string) {
	chatMessages.WithLabelValues(platform, outcome).Inc()
}

// warn reports a failure nobody is waiting to hear about to the engine's
// logger. hermod_chat_messages_total counts it whether or not there is one.
func (h *ChatHandler) warn(msg string, keysAndValues ...any) {
	if h.Registry == nil {
		return
	}
	if l := h.Registry.GetLogger(); l != nil {
		l.Warn(msg, keysAndValues...)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
