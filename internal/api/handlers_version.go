package api

import (
	"encoding/json"
	"net/http"

	"github.com/user/hermod/internal/version"
)

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"version": version.Version,
	})
}
