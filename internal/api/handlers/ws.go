package handlers

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/engine/telemetry"
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

const (
	// wsHeartbeat is the interval between server-initiated WebSocket ping
	// frames. It must be comfortably below the idle timeout of any intermediary
	// proxy (e.g. Cloudflare drops idle WebSocket connections after ~100s) so
	// the connection is kept alive end-to-end.
	wsHeartbeat = 30 * time.Second

	// wsPongWait is how long we wait for a pong (or any inbound frame) before
	// treating the peer as gone. It must be larger than wsHeartbeat so a single
	// missed pong does not tear down a healthy connection.
	wsPongWait = 2*wsHeartbeat + 10*time.Second

	// wsWriteWait bounds a single write so a stalled peer or a half-open
	// connection behind a reverse proxy (such as Cloudflare) cannot block the
	// handler goroutine indefinitely and leak resources.
	wsWriteWait = 10 * time.Second

	// wsMaxMessageSize caps inbound frames. These endpoints only expect small
	// control frames and tiny JSON commands, so a small limit prevents a
	// malicious client from forcing large allocations.
	wsMaxMessageSize = 4096
)

// wsWriteJSON writes v as JSON, bounding the write with a deadline so a stalled
// peer (or an idle reverse proxy) cannot block the caller forever.
func wsWriteJSON(conn *websocket.Conn, v any) error {
	if err := conn.SetWriteDeadline(time.Now().Add(wsWriteWait)); err != nil {
		return err
	}
	return conn.WriteJSON(v)
}

// wsWritePing sends a ping control frame with a bounded write deadline.
func wsWritePing(conn *websocket.Conn) error {
	return conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait))
}

// startWSReadPump configures read deadlines and a pong handler, then starts a
// goroutine that drains inbound frames so the connection can process control
// frames (pong/close) and observe client disconnects. The returned channel is
// closed once the peer goes away or the connection becomes unreadable.
//
// This is required because hijacked WebSocket connections do not have their
// request context canceled when the client disconnects. Without an active
// reader the server cannot observe the close handshake and would keep the
// subscription (and its goroutine) alive until the next failed write, leaking
// resources for long-lived streams such as /api/ws/status.
func startWSReadPump(conn *websocket.Conn) <-chan struct{} {
	conn.SetReadLimit(wsMaxMessageSize)
	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	return done
}

func (h *Handler) HandleStatusWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	workflowID := strings.TrimSpace(r.URL.Query().Get("workflow_id"))

	// Actively read so we can observe pong/close frames and client disconnects.
	done := startWSReadPump(conn)

	var ch chan telemetry.StatusUpdate
	if workflowID != "" {
		ch = h.Registry.SubscribeWorkflowStatus(workflowID)
	} else {
		ch = h.Registry.SubscribeStatus()
	}
	defer h.Registry.UnsubscribeStatus(ch)

	// Send an initial snapshot of the current engine statuses so the UI reflects
	// the real-time backend state immediately on connect, even for idle workflows
	// that are not actively broadcasting status updates.
	for _, snapshot := range h.Registry.GetAllStatuses() {
		if workflowID != "" && !strings.EqualFold(snapshot.WorkflowID, workflowID) {
			continue
		}
		if err := wsWriteJSON(conn, snapshot); err != nil {
			return
		}
	}

	// Heartbeat
	ticker := time.NewTicker(wsHeartbeat)
	defer ticker.Stop()

	for {
		select {
		case update := <-ch:
			if workflowID != "" && !strings.EqualFold(update.WorkflowID, workflowID) {
				continue
			}
			if err := wsWriteJSON(conn, update); err != nil {
				return
			}
		case <-ticker.C:
			if err := wsWritePing(conn); err != nil {
				return
			}
		case <-done:
			return
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

	// Actively read so we can observe pong/close frames and client disconnects.
	done := startWSReadPump(conn)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Heartbeat
	pingTicker := time.NewTicker(wsHeartbeat)
	defer pingTicker.Stop()

	for {
		select {
		case <-ticker.C:
			stats, err := h.Registry.GetDashboardStats(r.Context(), vhost)
			if err != nil {
				continue
			}
			if err := wsWriteJSON(conn, stats); err != nil {
				return
			}
		case <-pingTicker.C:
			if err := wsWritePing(conn); err != nil {
				return
			}
		case <-done:
			return
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

	// Actively read so we can observe pong/close frames and client disconnects.
	done := startWSReadPump(conn)

	var ch chan storage.Log
	if workflowID != "" {
		ch = h.Registry.SubscribeWorkflowLogs(workflowID)
	} else {
		ch = h.Registry.SubscribeLogs()
	}
	defer h.Registry.UnsubscribeLogs(ch)

	// Send initial logs
	filter := storage.LogFilter{
		WorkflowID: workflowID,
	}
	filter.Limit = 100
	initialLogs, _, err := h.Storage.ListLogs(r.Context(), filter)
	if err == nil {
		if err := wsWriteJSON(conn, initialLogs); err != nil {
			return
		}
	}

	// Heartbeat
	ticker := time.NewTicker(wsHeartbeat)
	defer ticker.Stop()

	for {
		select {
		case log, ok := <-ch:
			if !ok {
				return
			}
			if err := wsWriteJSON(conn, log); err != nil {
				return
			}
		case <-ticker.C:
			if err := wsWritePing(conn); err != nil {
				return
			}
		case <-done:
			return
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

	// Actively read so we can observe pong/close frames and client disconnects.
	done := startWSReadPump(conn)

	var ch chan registry.LiveMessage
	if workflowID != "" {
		ch = h.Registry.SubscribeWorkflowLiveMessages(workflowID)
	} else {
		ch = h.Registry.SubscribeLiveMessages()
	}
	defer h.Registry.UnsubscribeLiveMessages(ch)

	// Heartbeat
	ticker := time.NewTicker(wsHeartbeat)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			// When using per-workflow subscription, we already filtered by workflowID.
			// However, we still want to allow "test" messages if we are in test mode or if it's a global subscription.
			if workflowID != "" && !strings.EqualFold(msg.WorkflowID, workflowID) && !strings.EqualFold(msg.WorkflowID, "test") {
				continue
			}
			if err := wsWriteJSON(conn, msg); err != nil {
				return
			}
		case <-ticker.C:
			if err := wsWritePing(conn); err != nil {
				return
			}
		case <-done:
			return
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

	// Keep-alive configuration so the connection survives idle periods behind a
	// reverse proxy (e.g. Cloudflare) and so we can observe pong/close frames.
	conn.SetReadLimit(wsMaxMessageSize)
	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})

	// Listen for commands from the UI. When the read loop exits (client
	// disconnect, read error, or read timeout) we close done so the writer
	// goroutine can stop instead of leaking until the next failed write.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var cmd struct {
				Action string `json:"action"`
				MsgID  string `json:"msg_id"`
			}
			if err := conn.ReadJSON(&cmd); err != nil {
				return
			}
			// A valid command also proves the peer is alive, so extend the deadline.
			_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
			h.Registry.DebuggerCommand(workflowID, cmd.MsgID, cmd.Action)
		}
	}()

	// Heartbeat
	ticker := time.NewTicker(wsHeartbeat)
	defer ticker.Stop()

	for {
		select {
		case ev := <-ch:
			if err := wsWriteJSON(conn, ev); err != nil {
				return
			}
		case <-ticker.C:
			if err := wsWritePing(conn); err != nil {
				return
			}
		case <-done:
			return
		case <-r.Context().Done():
			return
		}
	}
}
