package api

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
		origin := r.Header.Get("Origin")
		if origin == "" {
			return false
		}

		allow := strings.Split(os.Getenv("HERMOD_CORS_ALLOW_ORIGINS"), ",")
		for i := range allow {
			allow[i] = strings.TrimSpace(allow[i])
		}
		for _, a := range allow {
			if a != "" && a == origin {
				return true
			}
		}

		if os.Getenv("HERMOD_ENV") != "production" {
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
		}

		return false
	},
}

func (s *Server) handleStatusWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ch := s.registry.SubscribeStatus()
	defer s.registry.UnsubscribeStatus(ch)

	for {
		select {
		case update := <-ch:
			if err := conn.WriteJSON(update); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleDashboardWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			stats, err := s.registry.GetDashboardStats(r.Context())
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

func (s *Server) handleLogsWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	query := r.URL.Query()
	workflowID := query.Get("workflow_id")

	// Send initial logs
	filter := storage.LogFilter{
		WorkflowID: workflowID,
	}
	filter.Limit = 100
	initialLogs, _, err := s.storage.ListLogs(r.Context(), filter)
	if err == nil {
		if err := conn.WriteJSON(initialLogs); err != nil {
			return
		}
	}

	ch := s.registry.SubscribeLogs()
	defer s.registry.UnsubscribeLogs(ch)

	for {
		select {
		case log := <-ch:
			if workflowID != "" && log.WorkflowID != workflowID {
				continue
			}
			if err := conn.WriteJSON(log); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}
