package http

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/api/handlers"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/comm/source/graphql"
	"github.com/user/hermod/pkg/comm/source/webhook"
	"github.com/user/hermod/pkg/infra/compression"
)

// authenticateWebhook verifies the HMAC signature of an incoming webhook against
// the secret configured on the matching source. It returns an HTTP status and a
// client-facing message; an empty message means authentication succeeded.
func (h *WebhookHandler) authenticateWebhook(r *http.Request, fullPath string, body []byte) (int, string) {
	sources, _, err := h.Storage.ListSources(r.Context(), storage.CommonFilter{})
	if err != nil {
		return http.StatusOK, ""
	}

	for _, src := range sources {
		if src.Type != "webhook" || src.Config["path"] != fullPath {
			continue
		}
		secret := src.Config["secret"]
		if secret == "" {
			return http.StatusOK, ""
		}
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
		return http.StatusOK, ""
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

	msg := message.AcquireMessage()
	msg.SetID(uuid.New().String())
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

	// Authenticate the request against the configured source secret (if any).
	if status, authMsg := h.authenticateWebhook(r, fullPath, body); authMsg != "" {
		message.ReleaseMessage(msg)
		h.JsonError(w, authMsg, status)
		return
	}

	if err := h.dispatchWebhook(r, fullPath, msg); err != nil {
		message.ReleaseMessage(msg)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	writeDispatched(w, r, msg.ID())
}

// authenticateGraphQL validates the X-API-Key header against the api_key
// configured on the matching GraphQL source using a constant-time comparison.
// It returns true when the request is authorized (including when no key is set).
func (h *WebhookHandler) authenticateGraphQL(r *http.Request, fullPath string) bool {
	sources, _, err := h.Storage.ListSources(r.Context(), storage.CommonFilter{})
	if err != nil {
		return true
	}
	var apiKey string
	for _, src := range sources {
		if src.Type == "graphql" && src.Config["path"] == fullPath {
			apiKey = src.Config["api_key"]
			break
		}
	}
	if apiKey == "" {
		return true
	}
	return handlers.ConstantTimeCompare(r.Header.Get("X-API-Key"), apiKey)
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
	if !h.authenticateGraphQL(r, fullPath) {
		message.ReleaseMessage(msg)
		h.JsonError(w, "Unauthorized", http.StatusUnauthorized)
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
