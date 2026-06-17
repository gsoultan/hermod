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

	workflowID := strings.TrimSpace(r.URL.Query().Get("workflow_id"))

	ch := h.Registry.SubscribeStatus()
	defer h.Registry.UnsubscribeStatus(ch)

	// Send an initial snapshot of the current engine statuses so the UI reflects
	// the real-time backend state immediately on connect, even for idle workflows
	// that are not actively broadcasting status updates.
	for _, snapshot := range h.Registry.GetAllStatuses() {
		if workflowID != "" && !strings.EqualFold(snapshot.WorkflowID, workflowID) {
			continue
		}
		if err := conn.WriteJSON(snapshot); err != nil {
			return
		}
	}

	// Heartbeat
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case update := <-ch:
			if workflowID != "" && !strings.EqualFold(update.WorkflowID, workflowID) {
				continue
			}
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
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Heartbeat
	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

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
		case <-pingTicker.C:
			if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second)); err != nil {
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
		case msg, ok := <-ch:
			if !ok {
				return
			}
			// Match workflow ID (case-insensitive) or allow "test" messages if we are in test mode
			// If workflowID is provided, we also allow "test" messages because they belong to the current editor's simulation
			if workflowID != "" && !strings.EqualFold(msg.WorkflowID, workflowID) && !strings.EqualFold(msg.WorkflowID, "test") {
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

func (h *Handler) HandleDebuggerWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	workflowID := r.URL.Query().Get("workflow_id")

	// Subscribe to debugger events
	ch := h.Registry.SubscribeDebugger(workflowID)
	defer h.Registry.UnsubscribeDebugger(workflowID, ch)

	// Listen for commands from the UI
	go func() {
		for {
			var cmd struct {
				Action string `json:"action"`
				MsgID  string `json:"msg_id"`
			}
			if err := conn.ReadJSON(&cmd); err != nil {
				return
			}
			h.Registry.DebuggerCommand(workflowID, cmd.MsgID, cmd.Action)
		}
	}()

	for {
		select {
		case ev := <-ch:
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}
