package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/source/webhook"
)

// Prometheus metrics for WebSocket server endpoints
var (
	wsInConnections = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hermod_ws_in_connections_total",
		Help: "Total number of WS producer connections (inbound).",
	})
	wsInMessages = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hermod_ws_in_messages_total",
		Help: "Total number of WS producer frames ingested as messages.",
	})
	wsInErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hermod_ws_in_errors_total",
		Help: "Total number of WS inbound errors (upgrade/read/dispatch).",
	})
	wsOutConnections = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hermod_ws_out_connections_total",
		Help: "Total number of WS subscriber connections (outbound).",
	})
	wsOutMessages = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hermod_ws_out_messages_total",
		Help: "Total WS messages sent to subscribers.",
	})
	wsOutErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hermod_ws_out_errors_total",
		Help: "Total WS outbound errors (upgrade/write).",
	})
)

func init() {
	// Best-effort register; ignore duplicate registrations when hot reloading in dev
	_ = prometheus.Register(wsInConnections)
	_ = prometheus.Register(wsInMessages)
	_ = prometheus.Register(wsInErrors)
	_ = prometheus.Register(wsOutConnections)
	_ = prometheus.Register(wsOutMessages)
	_ = prometheus.Register(wsOutErrors)
}

// handleWSIn upgrades to a WebSocket and treats each incoming frame as a message
// dispatched into the internal webhook bus at path /api/ws/in/{path...}.
func (s *Server) handleWSIn(w http.ResponseWriter, r *http.Request) {
	// In dev allow any origin to simplify local testing
	if os.Getenv("HERMOD_ENV") != "production" {
		upgrader.CheckOrigin = func(r *http.Request) bool { return true }
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		wsInErrors.Inc()
		return
	}
	defer conn.Close()
	wsInConnections.Inc()

	// Basic heartbeat
	hb := 30 * time.Second
	_ = conn.SetReadDeadline(time.Now().Add(2*hb + 10*time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(2*hb + 10*time.Second))
	})
	ping := time.NewTicker(hb)
	defer ping.Stop()

	path := r.PathValue("path")
	if path == "" {
		path = "default"
	}
	fullPath := "/api/ws/in/" + path

	type env struct {
		ID       string            `json:"id"`
		Op       string            `json:"op"`
		Table    string            `json:"table,omitempty"`
		Schema   string            `json:"schema,omitempty"`
		Payload  json.RawMessage   `json:"payload,omitempty"`
		Metadata map[string]string `json:"metadata,omitempty"`
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			// Best-effort ping
			_ = conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
		default:
		}

		msgType, data, err := conn.ReadMessage()
		if err != nil {
			wsInErrors.Inc()
			return
		}
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}

		// Try JSON envelope first
		var e env
		var m *message.DefaultMessage
		if json.Unmarshal(data, &e) == nil && (len(e.Payload) > 0 || e.Op != "" || len(e.Metadata) > 0) {
			m = message.AcquireMessage()
			if e.ID != "" {
				m.SetMetadata("ws_id", e.ID)
			}
			if len(e.Payload) > 0 {
				m.SetPayload(e.Payload)
			}
			switch strings.ToLower(e.Op) {
			case string(hermod.OpCreate):
				m.SetOperation(hermod.OpCreate)
			case string(hermod.OpUpdate):
				m.SetOperation(hermod.OpUpdate)
			case string(hermod.OpDelete):
				m.SetOperation(hermod.OpDelete)
			case string(hermod.OpSnapshot):
				m.SetOperation(hermod.OpSnapshot)
			default:
				m.SetOperation(hermod.OpCreate)
			}
			if e.Table != "" {
				m.SetTable(e.Table)
			}
			if e.Schema != "" {
				m.SetSchema(e.Schema)
			}
			for k, v := range e.Metadata {
				m.SetMetadata(k, v)
			}
		} else {
			// Treat entire frame as payload
			m = message.AcquireMessage()
			m.SetID(uuid.New().String())
			m.SetOperation(hermod.OpCreate)
			m.SetTable("websocket")
			m.SetAfter(data)
		}

		// Store inbound for replay/debug (mirrors HTTP webhook behavior)
		_ = s.storage.CreateWebhookRequest(r.Context(), storage.WebhookRequest{
			Timestamp: time.Now(),
			Path:      fullPath,
			Method:    "WS",
			Headers:   map[string]string{"User-Agent": r.Header.Get("User-Agent")},
			Body:      data,
		})

		if err := webhook.Dispatch(fullPath, m); err != nil {
			// Try to wake workflow then retry dispatch once
			if s.wakeUpWorkflow(r.Context(), "webhook", fullPath) {
				if err2 := webhook.Dispatch(fullPath, m); err2 == nil {
					wsInMessages.Inc()
					// Optional ACK
					if id := m.ID(); id != "" {
						_ = conn.WriteJSON(map[string]any{"ack": id, "ok": true})
					}
					continue
				}
			}
			message.ReleaseMessage(m)
			wsInErrors.Inc()
			// Best-effort error frame with redaction of details
			_ = conn.WriteJSON(map[string]any{"ok": false, "error": "dispatch_failed"})
			continue
		}
		wsInMessages.Inc()
		if id := m.ID(); id != "" {
			_ = conn.WriteJSON(map[string]any{"ack": id, "ok": true})
		}
	}
}

// handleWSOut upgrades to a WebSocket and streams live messages for a workflow.
func (s *Server) handleWSOut(w http.ResponseWriter, r *http.Request) {
	// In dev allow any origin
	if os.Getenv("HERMOD_ENV") != "production" {
		upgrader.CheckOrigin = func(r *http.Request) bool { return true }
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		wsOutErrors.Inc()
		return
	}
	defer conn.Close()
	wsOutConnections.Inc()

	workflowID := r.PathValue("workflowID")
	if strings.TrimSpace(workflowID) == "" {
		// Fallback to query param for flexibility
		workflowID = r.URL.Query().Get("workflow_id")
	}

	// Heartbeat
	hb := 30 * time.Second
	_ = conn.SetReadDeadline(time.Now().Add(2*hb + 10*time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(2*hb + 10*time.Second)) })
	ping := time.NewTicker(hb)
	defer ping.Stop()

	// Subscribe to live messages and filter by workflow
	ch := s.registry.SubscribeLiveMessages()
	defer s.registry.UnsubscribeLiveMessages(ch)

	type outEnv struct {
		WorkflowID string                 `json:"workflow_id"`
		NodeID     string                 `json:"node_id"`
		Timestamp  time.Time              `json:"timestamp"`
		Data       map[string]interface{} `json:"data"`
		IsError    bool                   `json:"is_error"`
		Error      string                 `json:"error,omitempty"`
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			_ = conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
		case evt := <-ch:
			if workflowID != "" && evt.WorkflowID != workflowID {
				continue
			}
			env := outEnv{
				WorkflowID: evt.WorkflowID,
				NodeID:     evt.NodeID,
				Timestamp:  evt.Timestamp,
				Data:       evt.Data,
				IsError:    evt.IsError,
				Error:      evt.Error,
			}
			if err := conn.WriteJSON(env); err != nil {
				wsOutErrors.Inc()
				return
			}
			wsOutMessages.Inc()
		}
	}
}
