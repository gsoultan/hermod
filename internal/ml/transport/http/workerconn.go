package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// probeTimeout bounds one Test connection: long enough for a worker on a
// slow network, short enough that a wrong address answers while the person
// still waits for it.
const probeTimeout = 15 * time.Second

// workerConnection is the ML worker connection as the console sees it. The
// token is never returned, only whether one is set.
type workerConnection struct {
	// Source is environment, saved or none.
	Source   worker.Source `json:"source"`
	URL      string        `json:"url"`
	TokenSet bool          `json:"token_set"`
	// Editable is false when the environment names the worker, which wins.
	Editable bool `json:"editable"`
}

// connectionRequest is a worker to test or save. Token absent keeps (or, for
// a test, uses) the saved token.
type connectionRequest struct {
	URL   string  `json:"url"`
	Token *string `json:"token"`
}

// workerProvider is the provider of the ML worker in force: worker.Default
// unless a test replaced it.
func (h *Handler) workerProvider() *worker.Provider {
	if h.workers != nil {
		return h.workers
	}
	return worker.Default
}

// adminOnly refuses anyone but an administrator. The routes are behind
// AdminOnly as well; this keeps the handler safe wherever it is mounted.
func (h *Handler) adminOnly(w http.ResponseWriter, r *http.Request) bool {
	user, _ := r.Context().Value(handlers.UserContextKey).(*storage.User)
	if user == nil || user.Role != storage.RoleAdministrator {
		h.JsonError(w, "Forbidden: connecting the ML worker needs the Administrator role", http.StatusForbidden)
		return false
	}
	return true
}

// GetWorkerConnection answers where the ML worker comes from and its address.
func (h *Handler) GetWorkerConnection(w http.ResponseWriter, r *http.Request) {
	if !h.adminOnly(w, r) {
		return
	}
	c, err := h.workerConnection(r.Context())
	if err != nil {
		h.failConnection(w, err)
		return
	}
	writeJSON(w, c)
}

func (h *Handler) workerConnection(ctx context.Context) (workerConnection, error) {
	p := h.workerProvider()
	if env := p.Env(); env != nil {
		return workerConnection{Source: worker.SourceEnvironment, URL: env.URL, TokenSet: env.Token != ""}, nil
	}
	saved, err := ml.LoadWorkerConnection(ctx, h.Storage)
	if err != nil {
		return workerConnection{}, err
	}
	c := workerConnection{Source: worker.SourceNone, Editable: true}
	if saved.URL != "" {
		c.Source, c.URL, c.TokenSet = worker.SourceSaved, saved.URL, saved.Token != ""
	}
	return c, nil
}

// PutWorkerConnection saves the ML worker and puts it in force at once, for
// training, datasets, the Collect Dataset sink and the worker status. An
// empty url disconnects it.
func (h *Handler) PutWorkerConnection(w http.ResponseWriter, r *http.Request) {
	if !h.adminOnly(w, r) {
		return
	}
	req, ok := h.decodeConnection(w, r)
	if !ok {
		return
	}
	saved, err := ml.SaveWorkerConnection(r.Context(), h.Storage, h.workerProvider(), req.URL, req.Token)
	if err != nil {
		h.failConnection(w, err)
		return
	}
	msg := "Connected the ML worker at " + saved.URL
	if saved.URL == "" {
		msg = "Disconnected the ML worker"
	}
	h.RecordAuditLog(r, "INFO", msg, "update", "", "", "",
		map[string]any{"ml_worker_url": saved.URL, "token_changed": req.Token != nil})
	h.GetWorkerConnection(w, r)
}

// CheckWorkerConnection calls the worker at the address given, with the token
// given (or the saved one), and says what it found: connected, with or
// without deep learning, or what is wrong. It is always 200: the worker
// refusing the token is the answer, not a failure of this call, and a 401
// would sign the administrator out of Hermod.
func (h *Handler) CheckWorkerConnection(w http.ResponseWriter, r *http.Request) {
	if !h.adminOnly(w, r) {
		return
	}
	req, ok := h.decodeConnection(w, r)
	if !ok {
		return
	}
	token := ""
	if req.Token != nil {
		token = *req.Token
	} else if saved, err := ml.LoadWorkerConnection(r.Context(), h.Storage); err == nil {
		token = saved.Token
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	writeJSON(w, worker.Probe(ctx, req.URL, token, nil))
}

func (h *Handler) decodeConnection(w http.ResponseWriter, r *http.Request) (connectionRequest, bool) {
	var req connectionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxModelBody)).Decode(&req); err != nil {
		h.JsonError(w, `the body must be {"url": "http://hermod-ml:8090", "token": "..."}`, http.StatusBadRequest)
		return req, false
	}
	return req, true
}

func (h *Handler) failConnection(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ml.ErrWorkerFromEnvironment):
		h.JsonError(w, err.Error(), http.StatusConflict)
	case errors.Is(err, ml.ErrBadWorkerURL):
		h.JsonError(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ml.ErrSettingsUnsupported):
		h.JsonError(w, err.Error(), http.StatusNotImplemented)
	default:
		h.JsonError(w, "could not save the ML worker connection: "+err.Error(), http.StatusInternalServerError)
	}
}

// workerSource says where the worker wk in force was configured: the
// environment when it names wk, else the console.
func (h *Handler) workerSource(wk *worker.Client) worker.Source {
	if env := h.workerProvider().Env(); env != nil && env.URL == wk.URL {
		return worker.SourceEnvironment
	}
	return worker.SourceSaved
}
