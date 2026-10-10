package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
)

// maxScriptBody bounds a saved script: its source plus a description.
const maxScriptBody = storage.MaxMLScriptBytes + 16<<10

// Custom training scripts are code that runs on a worker pool, so saving or
// deleting one needs the Administrator role. Reading one needs Editor on the
// vhost, as training with one does; a Viewer sees only names and hashes. All
// of it is refused while the server has custom scripts off.

func (h *Handler) ListScripts(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, false)
	if !ok {
		return
	}
	list, err := h.service().ListScripts(r.Context(), vhost)
	if err != nil {
		h.fail(w, err)
		return
	}
	if list == nil {
		list = []storage.MLScript{}
	}
	writeJSON(w, map[string]any{"data": list, "total": len(list)})
}

// GetScript returns a script's latest version with its source, and the list
// of its versions.
func (h *Handler) GetScript(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	name := r.PathValue("name")
	svc := h.service()
	sc, err := svc.Script(r.Context(), vhost, name)
	if err != nil {
		h.fail(w, err)
		return
	}
	versions, err := svc.ScriptVersions(r.Context(), vhost, name)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"script": sc, "versions": versions})
}

type scriptBody struct {
	Source      string `json:"source"`
	Description string `json:"description"`
}

// PutScript saves the body's source as the script's next version; the same
// source as the latest version saves nothing new.
func (h *Handler) PutScript(w http.ResponseWriter, r *http.Request) {
	vhost, user, ok := h.adminAccess(w, r)
	if !ok {
		return
	}
	var req scriptBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxScriptBody)).Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			h.JsonError(w, "the script is larger than 256 KB", http.StatusRequestEntityTooLarge)
			return
		}
		h.JsonError(w, `the body must be {"source": "...", "description": "..."}`, http.StatusBadRequest)
		return
	}
	name := r.PathValue("name")
	saved, err := h.service().PutScript(r.Context(), storage.MLScript{
		VHost: vhost, Name: name, Source: req.Source, Description: req.Description, CreatedBy: user.Username,
	})
	if err != nil {
		h.fail(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Saved training script "+name+" in vhost "+vhost, "update", vhost, "vhost", "",
		map[string]any{"script": name, "version": saved.Version, "sha256": saved.SHA256})
	saved.Source = ""
	writeJSON(w, saved)
}

// DeleteScript removes every version of a script. Model versions it trained
// keep their record of which script, by name and SHA-256, trained them.
func (h *Handler) DeleteScript(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.adminAccess(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if err := h.service().DeleteScript(r.Context(), vhost, name); err != nil {
		h.fail(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Deleted training script "+name+" in vhost "+vhost, "delete", vhost, "vhost", "",
		map[string]string{"script": name})
	w.WriteHeader(http.StatusNoContent)
}

// adminAccess is access for the Administrator role only. The route is behind
// AdminOnly too; this keeps the handler safe on its own.
func (h *Handler) adminAccess(w http.ResponseWriter, r *http.Request) (string, *storage.User, bool) {
	vhost, user, ok := h.access(w, r, true)
	if !ok {
		return "", nil, false
	}
	if user.Role != storage.RoleAdministrator {
		h.JsonError(w, "Forbidden: custom training scripts are saved and deleted by an Administrator", http.StatusForbidden)
		return "", nil, false
	}
	return vhost, user, true
}

// failPools maps the errors of custom scripts and worker pools onto a status;
// it reports whether it handled err.
func (h *Handler) failPools(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, ml.ErrCustomScriptsOff):
		h.JsonError(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ml.ErrNoCustomPool), errors.Is(err, ml.ErrNoGPUPool):
		h.JsonError(w, err.Error(), http.StatusServiceUnavailable)
	case errors.Is(err, ml.ErrScriptNotFound):
		h.JsonError(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ml.ErrBadTraining):
		h.JsonError(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ml.ErrScriptsUnsupported):
		h.JsonError(w, err.Error(), http.StatusNotImplemented)
	default:
		return false
	}
	return true
}
