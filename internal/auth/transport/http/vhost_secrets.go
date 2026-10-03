package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

// maxVHostSecretBody bounds a save request: the value limit, with room for the
// JSON around it and its escaping.
const maxVHostSecretBody = 2*storage.MaxVHostSecretValueLen + 1024

// vhostSecretAccess answers who is asking for which vhost's secrets, and
// refuses anyone who may not manage them: an Administrator may, and so may an
// Editor whose vhost list includes it. A Viewer may not even list the names --
// they are a map of what a tenant integrates with.
//
// There is no check that the vhost exists. "default" is a vhost every workflow
// may belong to without a row of its own, and a secret saved for a name nobody
// uses is read by nobody.
func (h *AuthHandler) vhostSecretAccess(w http.ResponseWriter, r *http.Request) (storage.VHostSecretStore, string, *storage.User, bool) {
	user, _ := r.Context().Value(handlers.UserContextKey).(*storage.User)
	if user == nil || (user.Role != storage.RoleAdministrator && user.Role != storage.RoleEditor) {
		h.JsonError(w, "Forbidden: managing a vhost's secrets needs the Editor role", http.StatusForbidden)
		return nil, "", nil, false
	}
	vhost := r.PathValue("vhost")
	if vhost == "" || vhost == "all" {
		h.JsonError(w, "a secret belongs to one vhost: name it", http.StatusBadRequest)
		return nil, "", nil, false
	}
	if user.Role != storage.RoleAdministrator && !h.HasVHostAccess(vhost, user.VHosts) {
		h.JsonError(w, "Forbidden: you do not have access to this vhost", http.StatusForbidden)
		return nil, "", nil, false
	}
	store, ok := h.Storage.(storage.VHostSecretStore)
	if !ok {
		h.JsonError(w, storage.ErrVHostSecretsUnsupported.Error(), http.StatusNotImplemented)
		return nil, "", nil, false
	}
	return store, vhost, user, true
}

// ListVHostSecrets lists a vhost's secrets: names, and who changed them when.
// Never a value.
func (h *AuthHandler) ListVHostSecrets(w http.ResponseWriter, r *http.Request) {
	store, vhost, _, ok := h.vhostSecretAccess(w, r)
	if !ok {
		return
	}
	list, err := store.ListVHostSecrets(r.Context(), vhost)
	if err != nil {
		h.JsonError(w, "Failed to list secrets", http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []storage.VHostSecret{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": list, "total": len(list)})
}

// PutVHostSecret creates a secret or replaces its value. The value is never
// echoed, logged or audited; the audit entry names the secret.
func (h *AuthHandler) PutVHostSecret(w http.ResponseWriter, r *http.Request) {
	store, vhost, user, ok := h.vhostSecretAccess(w, r)
	if !ok {
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxVHostSecretBody)).Decode(&req); err != nil {
		h.JsonError(w, `the request body must be JSON: {"value": "..."}`, http.StatusBadRequest)
		return
	}
	secret := storage.VHostSecret{VHost: vhost, Name: r.PathValue("name"), Value: req.Value, UpdatedBy: user.Username}
	if err := storage.ValidateVHostSecret(secret); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := store.PutVHostSecret(r.Context(), secret); err != nil {
		h.JsonError(w, "Failed to save the secret", http.StatusInternalServerError)
		return
	}
	h.forgetCachedVHostSecret(vhost, secret.Name)
	h.RecordAuditLog(r, "INFO", "Saved secret "+secret.Name+" in vhost "+vhost, "update", vhost, "vhost", "",
		map[string]string{"secret": secret.Name})
	w.WriteHeader(http.StatusNoContent)
}

func (h *AuthHandler) DeleteVHostSecret(w http.ResponseWriter, r *http.Request) {
	store, vhost, _, ok := h.vhostSecretAccess(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	err := store.DeleteVHostSecret(r.Context(), vhost, name)
	if errors.Is(err, storage.ErrNotFound) {
		h.JsonError(w, "this vhost has no secret by that name", http.StatusNotFound)
		return
	}
	if err != nil {
		h.JsonError(w, "Failed to delete the secret", http.StatusInternalServerError)
		return
	}
	h.forgetCachedVHostSecret(vhost, name)
	h.RecordAuditLog(r, "INFO", "Deleted secret "+name+" in vhost "+vhost, "delete", vhost, "vhost", "",
		map[string]string{"secret": name})
	w.WriteHeader(http.StatusNoContent)
}

// forgetCachedVHostSecret makes a save or a delete visible to the next message
// rather than when the expression cache expires.
func (h *AuthHandler) forgetCachedVHostSecret(vhost, name string) {
	if h.Registry != nil {
		h.Registry.InvalidateVHostSecret(vhost, name)
	}
}

// GetVHostSecretForWorker returns one secret's value to a worker.
//
// A worker runs workflows in its own process and reaches storage through this
// API, so it has to be able to read the value its workflow names -- as it
// already reads decrypted connector configs. It is the one route that returns
// a value, and it answers a worker's token only: no role a person can hold,
// Administrator included, gets a value back.
func (h *AuthHandler) GetVHostSecretForWorker(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(handlers.UserContextKey).(*storage.User)
	if user == nil || !strings.HasPrefix(user.ID, "worker:") {
		h.JsonError(w, "Forbidden: only a worker reads a secret's value", http.StatusForbidden)
		return
	}
	store, ok := h.Storage.(storage.VHostSecretStore)
	if !ok {
		h.JsonError(w, storage.ErrVHostSecretsUnsupported.Error(), http.StatusNotImplemented)
		return
	}
	secret, err := store.GetVHostSecret(r.Context(), r.PathValue("vhost"), r.PathValue("name"))
	if errors.Is(err, storage.ErrNotFound) {
		h.JsonError(w, "this vhost has no secret by that name", http.StatusNotFound)
		return
	}
	if err != nil {
		h.JsonError(w, "Failed to read the secret", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{"value": secret.Value})
}
