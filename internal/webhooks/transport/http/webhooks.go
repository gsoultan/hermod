package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/reply"
	"github.com/gsoultan/hermod/pkg/comm/source/graphql"
	"github.com/gsoultan/hermod/pkg/comm/source/webhook"
	"github.com/gsoultan/hermod/pkg/infra/compression"
)

// sourceConfig returns the configuration of the source of the given type that
// holds fullPath, or nil when no source does.
//
// An error means the store could not be read, which is not the same as there
// being no such source. The configuration is where a source's credentials are,
// so a caller that cannot read it does not know whether the path has any, and
// must refuse the request rather than treat it as an open endpoint.
func (h *WebhookHandler) sourceConfig(r *http.Request, sourceType, fullPath string) (map[string]string, error) {
	sources, _, err := h.Storage.ListSources(r.Context(), storage.CommonFilter{})
	if err != nil {
		return nil, err
	}
	for _, src := range sources {
		if src.Type == sourceType && src.Config["path"] == fullPath {
			return src.Config, nil
		}
	}
	return nil, nil
}

// msgCannotAuthenticate is what a caller is told when the store holding an
// endpoint's credentials cannot be read.
const msgCannotAuthenticate = "the endpoint's credentials cannot be checked right now"

// authenticateWebhook holds an incoming webhook to the credentials configured
// on its source: an API key, sent as X-API-Key, and an HMAC signing secret,
// sent as X-Hub-Signature-256 or X-Webhook-Signature. A source may have
// either, both or neither, and every one it has must be satisfied. It returns
// an HTTP status and a client-facing message; an empty message means the
// request is authenticated.
//
// The API key is the credential the source form offers. It was saved and never
// checked: only the signing secret was, and no field in the form writes one.
func (h *WebhookHandler) authenticateWebhook(r *http.Request, config map[string]string, body []byte) (int, string) {
	if key := config["api_key"]; key != "" && !handlers.ConstantTimeCompare(r.Header.Get("X-API-Key"), key) {
		return http.StatusUnauthorized, "Invalid API key"
	}
	if secret := config["secret"]; secret != "" {
		signature := r.Header.Get("X-Hub-Signature-256")
		if signature == "" {
			signature = r.Header.Get("X-Webhook-Signature")
		}
		if signature == "" {
			return http.StatusUnauthorized, "Missing signature"
		}
		if !handlers.VerifyWebhookSignature(secret, body, signature) {
			return http.StatusUnauthorized, "Invalid signature"
		}
	}
	return http.StatusOK, ""
}

// readRequestBody reads the request body and transparently decompresses it when
// a supported Content-Encoding is present.
func readRequestBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	encoding := r.Header.Get("Content-Encoding")
	if encoding == "" {
		return body, nil
	}
	comp, err := compression.NewCompressor(compression.Algorithm(encoding))
	if err != nil {
		// Unknown encoding: pass the body through unchanged.
		return body, nil //nolint:nilerr // unknown encodings are intentionally treated as plain bodies
	}
	return comp.Decompress(body)
}

// dispatchWebhook delivers the message to the webhook source, waking a parked
// workflow and retrying once if the initial dispatch finds no listener.
func (h *WebhookHandler) dispatchWebhook(r *http.Request, fullPath string, msg hermod.Message) error {
	err := webhook.Dispatch(fullPath, msg)
	if err == nil {
		return nil
	}
	if h.WakeUpWorkflow(r.Context(), "webhook", fullPath) {
		if retryErr := webhook.Dispatch(fullPath, msg); retryErr == nil {
			return nil
		}
	}
	return err
}

// writeDispatched writes the standard "dispatched" JSON acknowledgement.
func writeDispatched(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusAccepted)
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "dispatched", "id": id})
}

// collectHeaders flattens request headers into a string map for persistence.
func collectHeaders(r *http.Request) map[string]string {
	headers := make(map[string]string, len(r.Header))
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[k] = strings.Join(v, ", ")
		}
	}
	return headers
}

func (h *WebhookHandler) RegisterWebhookRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/webhooks/{path...}", h.HandleWebhook)
	mux.HandleFunc("GET /api/webhooks/{path...}", h.HandleWebhook)
}

func (h *WebhookHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if path == "" {
		http.Error(w, "Path is required", http.StatusBadRequest)
		return
	}

	// Full path for matching
	fullPath := "/api/webhooks/" + path

	body, err := readRequestBody(r)
	if err != nil {
		http.Error(w, "Failed to read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Read after dispatch, the message may already be the engine's and back in
	// the pool, so the id is kept here.
	id := uuid.New().String()
	msg := message.AcquireMessage()
	msg.SetID(id)
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable("webhook")
	msg.SetAfter(body)
	msg.SetMetadata("webhook_path", fullPath)
	msg.SetMetadata("http_method", r.Method)

	// Store webhook request for replay
	_ = h.Storage.CreateWebhookRequest(r.Context(), storage.WebhookRequest{
		Timestamp: time.Now(),
		Path:      fullPath,
		Method:    r.Method,
		Headers:   collectHeaders(r),
		Body:      body,
	})

	// Authenticate the request against its source's credentials, if any.
	config, err := h.sourceConfig(r, "webhook", fullPath)
	if err != nil {
		message.ReleaseMessage(msg)
		h.JsonError(w, msgCannotAuthenticate, http.StatusServiceUnavailable)
		return
	}
	if status, authMsg := h.authenticateWebhook(r, config, body); authMsg != "" {
		message.ReleaseMessage(msg)
		h.JsonError(w, authMsg, status)
		return
	}

	// A source set to answer synchronously holds the request until the
	// workflow has finished with the message. The waiter is registered before
	// the dispatch, or a fast workflow could finish first and answer nobody.
	wait, timeout := reply.ModeOf(config)
	conversation, ok := conversationID(r, wait)
	if !ok {
		message.ReleaseMessage(msg)
		h.JsonError(w, "Invalid "+reply.HeaderConversationID+": use 1 to 128 letters, digits, '.', '_', ':' or '-'", http.StatusBadRequest)
		return
	}
	if conversation != "" {
		msg.SetMetadata(reply.MetaConversationID, conversation)
		w.Header().Set(reply.HeaderConversationID, conversation)
	}
	var pending *reply.Pending
	if wait {
		p, err := reply.Expect(msg)
		if err != nil {
			message.ReleaseMessage(msg)
			h.JsonError(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		pending = p
		defer pending.Cancel()
	}

	if err := h.dispatchWebhook(r, fullPath, msg); err != nil {
		message.ReleaseMessage(msg)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if pending == nil {
		writeDispatched(w, r, id)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	outcome, err := pending.Wait(ctx)
	if err != nil {
		// The wait ran out, or the caller went away. The message is still the
		// workflow's and may yet be delivered, so this is not a failure: a
		// caller that retries a failure sends the record twice.
		writeOutcome(w, http.StatusAccepted, syncResponse{ID: id, Status: "pending", ConversationID: conversation})
		return
	}
	writeOutcome(w, outcomeStatus(outcome.Status), syncResponse{
		ID:             id,
		Status:         string(outcome.Status),
		Error:          outcome.Error,
		Record:         outcome.Record,
		ConversationID: conversation,
	})
}

// conversationID is the conversation the request belongs to: the one the
// caller names, or, for a caller that waits for its answer and named none, a
// new one it is told about. ok is false when the named id is not valid.
func conversationID(r *http.Request, wait bool) (id string, ok bool) {
	id = r.Header.Get(reply.HeaderConversationID)
	switch {
	case id != "":
		return id, reply.ValidConversationID(id)
	case wait:
		return reply.NewConversationID(), true
	default:
		return "", true
	}
}

// syncResponse is what a synchronous webhook answers with.
type syncResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	// Record is the message as the workflow left it.
	Record json.RawMessage `json:"record,omitempty"`
	// ConversationID is the chat this message belongs to, to send back as
	// X-Conversation-Id with the next message.
	ConversationID string `json:"conversation_id,omitempty"`
}

// outcomeStatus is the HTTP status for what the workflow did. A message that
// failed is a 502 whether or not it was parked in the dead-letter sink: either
// way it did not reach where the caller sent it, and the body says which.
func outcomeStatus(s reply.Status) int {
	switch s {
	case reply.Delivered, reply.Completed, reply.Filtered:
		return http.StatusOK
	default:
		return http.StatusBadGateway
	}
}

func writeOutcome(w http.ResponseWriter, status int, body syncResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// authenticateGraphQL validates the X-API-Key header against the api_key
// configured on the matching GraphQL source using a constant-time comparison.
// It returns an HTTP status and a client-facing message; an empty message
// means the request is authorized, which includes a source with no key.
func (h *WebhookHandler) authenticateGraphQL(r *http.Request, fullPath string) (int, string) {
	config, err := h.sourceConfig(r, "graphql", fullPath)
	if err != nil {
		return http.StatusServiceUnavailable, msgCannotAuthenticate
	}
	if key := config["api_key"]; key != "" && !handlers.ConstantTimeCompare(r.Header.Get("X-API-Key"), key) {
		return http.StatusUnauthorized, "Unauthorized"
	}
	return http.StatusOK, ""
}

func (h *WebhookHandler) HandleGraphQL(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if path == "" {
		path = "default"
	}
	fullPath := "/api/graphql/" + path

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusInternalServerError)
		return
	}

	msg := message.AcquireMessage()
	msg.SetID(uuid.New().String())
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable("graphql")
	msg.SetAfter(body)
	msg.SetMetadata("graphql_path", fullPath)
	msg.SetMetadata("http_method", r.Method)

	// Authenticate against the configured API key (if any).
	if status, authMsg := h.authenticateGraphQL(r, fullPath); authMsg != "" {
		message.ReleaseMessage(msg)
		h.JsonError(w, authMsg, status)
		return
	}

	if err := h.dispatchGraphQL(r, fullPath, msg); err != nil {
		message.ReleaseMessage(msg)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	writeDispatched(w, r, msg.ID())
}

// dispatchGraphQL delivers the message to the GraphQL source, waking a parked
// workflow and retrying once if the initial dispatch finds no listener.
func (h *WebhookHandler) dispatchGraphQL(r *http.Request, fullPath string, msg hermod.Message) error {
	err := graphql.Dispatch(fullPath, msg)
	if err == nil {
		return nil
	}
	if h.WakeUpWorkflow(r.Context(), "graphql", fullPath) {
		if retryErr := graphql.Dispatch(fullPath, msg); retryErr == nil {
			return nil
		}
	}
	return err
}
