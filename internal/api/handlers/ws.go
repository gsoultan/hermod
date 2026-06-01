package handlers

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/hermod/internal/storage"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// In dev allow any origin to simplify local testing
		if os.Getenv("HERMOD_ENV") != "production" {
			return true
		}

		origin := r.Header.Get("Origin")
		if origin == "" {
			// Allow requests without Origin header (often same-origin)
			return true
		}

		allow := strings.Split(os.Getenv("HERMOD_CORS_ALLOW_ORIGINS"), ",")
		for i := range allow {
			allow[i] = strings.TrimSpace(allow[i])
		}
		for _, a := range allow {
			if a != "" && (a == origin || a == "*") {
				return true
			}
		}

		// Also allow if it matches the Host header
		u, err := url.Parse(origin)
		if err == nil && u != nil {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			if u.Hostname() == host {
				return true
			}
		}

		return false
	},
}

func (h *Handler) HandleStatusWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ch := h.Registry.SubscribeStatus()
	defer h.Registry.UnsubscribeStatus(ch)

	// Heartbeat
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case update := <-ch:
			if err := conn.WriteJSON(update); err != nil {
				return
			}
		case <-ticker.C:
			if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (h *Handler) HandleDashboardWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	vhost := r.URL.Query().Get("vhost")
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			stats, err := h.Registry.GetDashboardStats(r.Context(), vhost)
			if err != nil {
				continue
			}
			if err := conn.WriteJSON(stats); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (h *Handler) HandleLogsWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	query := r.URL.Query()
	workflowID := strings.TrimSpace(query.Get("workflow_id"))

	// Send initial logs
	filter := storage.LogFilter{
		WorkflowID: workflowID,
	}
	filter.Limit = 100
	initialLogs, _, err := h.Storage.ListLogs(r.Context(), filter)
	if err == nil {
		if err := conn.WriteJSON(initialLogs); err != nil {
			return
		}
	}

	ch := h.Registry.SubscribeLogs()
	defer h.Registry.UnsubscribeLogs(ch)

	// Heartbeat
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case log := <-ch:
			if workflowID != "" && !strings.EqualFold(log.WorkflowID, workflowID) {
				continue
			}
			if err := conn.WriteJSON(log); err != nil {
				return
			}
		case <-ticker.C:
			if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (h *Handler) HandleLiveMessagesWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	query := r.URL.Query()
	workflowID := strings.TrimSpace(query.Get("workflow_id"))

	ch := h.Registry.SubscribeLiveMessages()
	defer h.Registry.UnsubscribeLiveMessages(ch)

	// Heartbeat
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case msg := <-ch:
			// Match workflow ID (case-insensitive) or allow "test" messages if we are in test mode
			if workflowID != "" && !strings.EqualFold(msg.WorkflowID, workflowID) {
				// Special case: if we're testing a workflow, it might broadcast with ID "test"
				// but we only want to show it if the current editor is also in some kind of test mode.
				// For now, we strictly match, but EqualFold helps with UUID casing.
				continue
			}
			if err := conn.WriteJSON(msg); err != nil {
				return
			}
		case <-ticker.C:
			if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}
