package http

import (
	"encoding/json"
	"net/http"

	"github.com/gsoultan/Hermod/internal/version"
)

func (h *InfraHandler) HandleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"version": version.Version,
	})
}
