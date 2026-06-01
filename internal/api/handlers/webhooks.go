package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/comm/source/graphql"
	"github.com/user/hermod/pkg/comm/source/webhook"
	"github.com/user/hermod/pkg/infra/compression"
)

func (h *Handler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if path == "" {
		http.Error(w, "Path is required", http.StatusBadRequest)
		return
	}

	// Full path for matching
	fullPath := "/api/webhooks/" + path

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusInternalServerError)
		return
	}

	// Handle compression
	encoding := r.Header.Get("Content-Encoding")
	if encoding != "" {
		comp, err := compression.NewCompressor(compression.Algorithm(encoding))
		if err == nil {
			decompressed, err := comp.Decompress(body)
			if err == nil {
				body = decompressed
			} else {
				http.Error(w, "Failed to decompress body: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
	}

	msg := message.AcquireMessage()
	msg.SetID(uuid.New().String())
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable("webhook")
	msg.SetAfter(body)
	msg.SetMetadata("webhook_path", fullPath)
	msg.SetMetadata("http_method", r.Method)

	// Store webhook request for replay
	headers := make(map[string]string)
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[k] = strings.Join(v, ", ")
		}
	}
	_ = h.Storage.CreateWebhookRequest(r.Context(), storage.WebhookRequest{
		Timestamp: time.Now(),
		Path:      fullPath,
		Method:    r.Method,
		Headers:   headers,
		Body:      body,
	})

	// Dispatch to the source
	// Find the source to check for secret
	sources, _, err := h.Storage.ListSources(r.Context(), storage.CommonFilter{})
	if err == nil {
		for _, src := range sources {
			if src.Type == "webhook" && src.Config["path"] == fullPath {
				secret := src.Config["secret"]
				if secret != "" {
					signature := r.Header.Get("X-Hub-Signature-256")
					if signature == "" {
						signature = r.Header.Get("X-Webhook-Signature")
					}

					if signature == "" {
						h.JsonError(w, "Missing signature", http.StatusUnauthorized)
						return
					}
				}
				break
			}
		}
	}

	if err := webhook.Dispatch(fullPath, msg); err != nil {
		// Attempt to wake up workflow if it was parked
		if h.WakeUpWorkflow(r.Context(), "webhook", fullPath) {
			if err := webhook.Dispatch(fullPath, msg); err == nil {
				goto dispatched
			}
		}
		message.ReleaseMessage(msg)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

dispatched:
	if r.Method == "GET" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "dispatched", "id": msg.ID()})
		return
	}

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "dispatched", "id": msg.ID()})
}

func (h *Handler) HandleGraphQL(w http.ResponseWriter, r *http.Request) {
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

	// Find the source to check for API key
	sources, _, err := h.Storage.ListSources(r.Context(), storage.CommonFilter{})
	if err == nil {
		var apiKey string
		for _, src := range sources {
			if src.Type == "graphql" && src.Config["path"] == fullPath {
				apiKey = src.Config["api_key"]
				break
			}
		}

		if apiKey != "" && r.Header.Get("X-API-Key") != apiKey {
			h.JsonError(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	if err := graphql.Dispatch(fullPath, msg); err != nil {
		// Attempt to wake up workflow
		if h.WakeUpWorkflow(r.Context(), "graphql", fullPath) {
			if err := graphql.Dispatch(fullPath, msg); err == nil {
				goto dispatched
			}
		}
		message.ReleaseMessage(msg)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

dispatched:
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "dispatched", "id": msg.ID()})
}
