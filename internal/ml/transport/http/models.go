// Package http is the REST surface of the model registry: managing a vhost's
// models, calling one from the UI, and the serving endpoint applications call
// with a model's serving key.
package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// maxModelBody bounds a model definition; maxPredictBody bounds a prediction
// request, which is at most ml.MaxRowsPerCall rows.
const (
	maxModelBody   = 256 << 10
	maxPredictBody = 8 << 20
)

// Handler serves the model routes.
type Handler struct {
	*handlers.Handler

	// worker replaces the ML worker the environment names, and noEnvWorker
	// drops it; tests set them.
	worker      *worker.Client
	noEnvWorker bool
}

// NewHandler wraps the shared API handler.
func NewHandler(h *handlers.Handler) *Handler {
	return &Handler{Handler: h}
}

// RegisterRoutes mounts the model routes. Writes are behind EditorOnly, and
// access() then checks the vhost in the path, which a role wrapper cannot.
// /api/ml/serve/ is public in AuthMiddleware: the serving key is its
// credential, checked in Serve.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/vhosts/{vhost}/ml/models", h.ListModels)
	mux.HandleFunc("GET /api/vhosts/{vhost}/ml/models/{name}", h.GetModel)
	mux.Handle("PUT /api/vhosts/{vhost}/ml/models/{name}", h.EditorOnly(h.PutModel))
	mux.Handle("DELETE /api/vhosts/{vhost}/ml/models/{name}", h.EditorOnly(h.DeleteModel))
	mux.Handle("POST /api/vhosts/{vhost}/ml/models/{name}/predict", h.EditorOnly(h.Predict))
	mux.Handle("POST /api/vhosts/{vhost}/ml/models/{name}/serving-key", h.EditorOnly(h.RotateServingKey))
	mux.Handle("DELETE /api/vhosts/{vhost}/ml/models/{name}/serving-key", h.EditorOnly(h.DisableServing))
	mux.Handle("PUT /api/vhosts/{vhost}/ml/models/{name}/mcp", h.EditorOnly(h.SetMCPExposure))
	mux.HandleFunc("GET /api/vhosts/{vhost}/ml/quotas", h.GetQuotas)
	mux.Handle("PUT /api/vhosts/{vhost}/ml/quotas", h.AdminOnly(h.PutQuotas))
	mux.HandleFunc("POST /api/ml/serve/{vhost}/{name}", h.Serve)

	mux.HandleFunc("GET /api/ml/worker", h.WorkerStatus)
	mux.HandleFunc("GET /api/vhosts/{vhost}/ml/datasets", h.ListDatasets)
	mux.Handle("GET /api/vhosts/{vhost}/ml/datasets/{name}", h.EditorOnly(h.GetDataset))
	mux.Handle("PUT /api/vhosts/{vhost}/ml/datasets/{name}/file", h.EditorOnly(h.UploadDataset))
	mux.Handle("POST /api/vhosts/{vhost}/ml/datasets/{name}/query", h.EditorOnly(h.DatasetFromQuery))
	mux.Handle("DELETE /api/vhosts/{vhost}/ml/datasets/{name}", h.EditorOnly(h.DeleteDataset))
	mux.Handle("POST /api/vhosts/{vhost}/ml/models/{name}/train", h.EditorOnly(h.TrainModel))
	mux.HandleFunc("GET /api/vhosts/{vhost}/ml/models/{name}/versions", h.ListVersions)
	mux.Handle("POST /api/vhosts/{vhost}/ml/models/{name}/versions/{version}/promote", h.EditorOnly(h.PromoteVersion))
}

// service is the registry's ML service when there is a registry, so a model's
// token secret is answered the way a workflow's is; otherwise one over the
// API's storage, which can still answer everything but a token.
func (h *Handler) service() *ml.Service {
	var svc *ml.Service
	if h.Registry != nil {
		svc = h.Registry.MLService()
	} else {
		svc = ml.NewService(func() any { return h.Storage }, nil, nil)
	}
	switch {
	case h.worker != nil:
		svc.WithWorker(h.worker)
	case h.noEnvWorker:
		svc.WithWorker(nil)
	}
	return svc
}

// access answers who is asking about which vhost, and refuses anyone who may
// not. Reading a vhost's models needs any role on the vhost; anything else —
// defining one, calling one, making its serving key — needs Editor.
func (h *Handler) access(w http.ResponseWriter, r *http.Request, write bool) (string, *storage.User, bool) {
	user, _ := r.Context().Value(handlers.UserContextKey).(*storage.User)
	if user == nil {
		h.JsonError(w, "Forbidden", http.StatusForbidden)
		return "", nil, false
	}
	if write && user.Role != storage.RoleAdministrator && user.Role != storage.RoleEditor {
		h.JsonError(w, "Forbidden: this needs the Editor role", http.StatusForbidden)
		return "", nil, false
	}
	vhost := r.PathValue("vhost")
	if vhost == "" || vhost == "all" {
		h.JsonError(w, "a model belongs to one vhost: name it", http.StatusBadRequest)
		return "", nil, false
	}
	if user.Role != storage.RoleAdministrator && !h.HasVHostAccess(vhost, user.VHosts) {
		h.JsonError(w, "Forbidden: you do not have access to this vhost", http.StatusForbidden)
		return "", nil, false
	}
	return vhost, user, true
}

// fail maps a service error onto a status. Errors from a model server are
// passed on as 502 with their text: the caller is debugging that server. A
// quota refusal is 429 when waiting is enough and 403 when it is not.
func (h *Handler) fail(w http.ResponseWriter, err error) {
	var quota *ml.QuotaError
	switch {
	case errors.As(err, &quota) && quota.Retryable:
		w.Header().Set("Retry-After", "1")
		h.JsonError(w, err.Error(), http.StatusTooManyRequests)
	case errors.As(err, &quota):
		h.JsonError(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, storage.ErrMLQuotasUnsupported):
		h.JsonError(w, err.Error(), http.StatusNotImplemented)
	case errors.Is(err, ml.ErrModelNotFound):
		h.JsonError(w, "this vhost has no model by that name", http.StatusNotFound)
	case errors.Is(err, ml.ErrVersionNotFound), errors.Is(err, worker.ErrNotFound):
		h.JsonError(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ml.ErrNoWorker):
		h.JsonError(w, err.Error(), http.StatusServiceUnavailable)
	case errors.Is(err, ml.ErrNoLiveVersion):
		h.JsonError(w, err.Error(), http.StatusConflict)
	case errors.Is(err, worker.ErrBusy):
		h.JsonError(w, err.Error(), http.StatusTooManyRequests)
	case errors.Is(err, storage.ErrMLModelsUnsupported):
		h.JsonError(w, err.Error(), http.StatusNotImplemented)
	case errors.Is(err, ml.ErrTooManyRows):
		h.JsonError(w, err.Error(), http.StatusBadRequest)
	default:
		h.JsonError(w, err.Error(), http.StatusBadGateway)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handler) ListModels(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, false)
	if !ok {
		return
	}
	ms, err := h.service().Models()
	if err != nil {
		h.fail(w, err)
		return
	}
	list, err := ms.ListMLModels(r.Context(), vhost)
	if err != nil {
		h.JsonError(w, "Failed to list models", http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []storage.MLModel{}
	}
	writeJSON(w, map[string]any{"data": list, "total": len(list)})
}

func (h *Handler) GetModel(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, false)
	if !ok {
		return
	}
	m, err := h.service().Model(r.Context(), vhost, r.PathValue("name"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, m)
}

// modelRequest is what a client may set on a model. VHost, name, the serving
// key and the bookkeeping come from the path and the server, never the body.
// MCPExposed left out keeps what the model has; the model form does not send
// it, and the Expose to MCP switch uses its own route.
type modelRequest struct {
	Description   string            `json:"description"`
	Backend       inference.Backend `json:"backend"`
	URL           string            `json:"url"`
	RemoteModel   string            `json:"remote_model"`
	RemoteVersion string            `json:"remote_version"`
	TokenSecret   string            `json:"token_secret"`
	InputName     string            `json:"input_name"`
	Features      []string          `json:"features"`
	TimeoutMs     int               `json:"timeout_ms"`
	MCPExposed    *bool             `json:"mcp_exposed"`
}

func (h *Handler) PutModel(w http.ResponseWriter, r *http.Request) {
	vhost, user, ok := h.access(w, r, true)
	if !ok {
		return
	}
	var req modelRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxModelBody)).Decode(&req); err != nil {
		h.JsonError(w, "the request body must be a JSON model definition", http.StatusBadRequest)
		return
	}
	m := storage.MLModel{
		VHost: vhost, Name: r.PathValue("name"), Description: req.Description,
		Backend: req.Backend, URL: strings.TrimSpace(req.URL), RemoteModel: req.RemoteModel, RemoteVersion: req.RemoteVersion,
		TokenSecret: req.TokenSecret, InputName: req.InputName, Features: req.Features, TimeoutMs: req.TimeoutMs,
		UpdatedBy: user.Username,
	}
	if err := storage.ValidateMLModel(m); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	svc := h.service()
	ms, err := svc.Models()
	if err != nil {
		h.fail(w, err)
		return
	}
	if old, err := ms.GetMLModel(r.Context(), vhost, m.Name); err == nil {
		m.MCPExposed = old.MCPExposed
		m.FeatureTypes = keptTypes(old.FeatureTypes, m.Features)
	}
	if req.MCPExposed != nil {
		m.MCPExposed = *req.MCPExposed
	}
	if err := svc.PutModel(r.Context(), m); err != nil {
		var quota *ml.QuotaError
		if errors.As(err, &quota) {
			h.fail(w, err)
			return
		}
		h.JsonError(w, "Failed to save the model", http.StatusInternalServerError)
		return
	}
	h.RecordAuditLog(r, "INFO", "Saved model "+m.Name+" in vhost "+vhost, "update", vhost, "vhost", "",
		map[string]string{"model": m.Name, "backend": string(m.Backend)})
	saved, err := ms.GetMLModel(r.Context(), vhost, m.Name)
	if err != nil {
		saved = m
	}
	writeJSON(w, saved)
}

// keptTypes is the known types of the features a model still declares.
func keptTypes(types map[string]string, features []string) map[string]string {
	if len(types) == 0 {
		return nil
	}
	out := make(map[string]string, len(features))
	for _, f := range features {
		if t, ok := types[f]; ok {
			out[f] = t
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (h *Handler) DeleteModel(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if err := h.service().DeleteModel(r.Context(), vhost, name); err != nil {
		if errors.Is(err, ml.ErrModelNotFound) || errors.Is(err, storage.ErrMLModelsUnsupported) {
			h.fail(w, err)
			return
		}
		h.JsonError(w, "Failed to delete the model: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.RecordAuditLog(r, "INFO", "Deleted model "+name+" in vhost "+vhost, "delete", vhost, "vhost", "",
		map[string]string{"model": name})
	w.WriteHeader(http.StatusNoContent)
}

// RotateServingKey makes a new serving key and returns it, once.
func (h *Handler) RotateServingKey(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	name := r.PathValue("name")
	key, err := h.service().RotateServingKey(r.Context(), vhost, name)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Made a new serving key for model "+name+" in vhost "+vhost, "update", vhost, "vhost", "",
		map[string]string{"model": name})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]string{"key": key})
}

// DisableServing removes the serving key; applications can no longer call the
// model until a new one is made.
func (h *Handler) DisableServing(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if err := h.service().DisableServing(r.Context(), vhost, name); err != nil {
		h.fail(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Turned off serving for model "+name+" in vhost "+vhost, "update", vhost, "vhost", "",
		map[string]string{"model": name})
	w.WriteHeader(http.StatusNoContent)
}

// predictRequest is the body of a prediction call, in the TensorFlow Serving
// "instances" shape: one object per row.
type predictRequest struct {
	Instances []inference.Row `json:"instances"`
}

// Predict calls a model from the UI, as an Editor of its vhost.
func (h *Handler) Predict(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	h.predict(w, r, vhost, r.PathValue("name"))
}

// Serve is how an application calls a model: no session, the model's serving
// key as X-API-Key or a bearer token. Every refusal is the same 401, so the
// endpoint does not say which models exist or which have serving turned on.
func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) {
	vhost, name := r.PathValue("vhost"), r.PathValue("name")
	key := r.Header.Get("X-API-Key")
	if key == "" {
		key = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	if key == "" || h.service().AuthorizeServing(r.Context(), vhost, name, key) != nil {
		h.JsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	h.predict(w, r, vhost, name)
}

func (h *Handler) predict(w http.ResponseWriter, r *http.Request, vhost, name string) {
	var req predictRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPredictBody)).Decode(&req); err != nil {
		h.JsonError(w, `the request body must be JSON: {"instances": [{...}, ...]}`, http.StatusBadRequest)
		return
	}
	if len(req.Instances) == 0 {
		h.JsonError(w, `"instances" must hold at least one row`, http.StatusBadRequest)
		return
	}
	preds, err := h.service().Predict(r.Context(), vhost, name, req.Instances)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"model": name, "predictions": preds})
}
