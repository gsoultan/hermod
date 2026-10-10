package http

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// retrainRequest is what a client may set on a retrain policy. Who set it and
// when come from the server.
type retrainRequest struct {
	Schedule string           `json:"schedule"`
	NewRows  int              `json:"new_rows"`
	Spec     worker.TrainSpec `json:"spec"`
	GoLive   ml.GoLive        `json:"go_live"`
}

// PutRetrain makes a trained model train again by itself: on a schedule,
// after its dataset grows by a number of rows, or both.
func (h *Handler) PutRetrain(w http.ResponseWriter, r *http.Request) {
	vhost, user, ok := h.access(w, r, true)
	if !ok {
		return
	}
	var req retrainRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxModelBody)).Decode(&req); err != nil {
		h.JsonError(w, "the request body must be a JSON retrain policy", http.StatusBadRequest)
		return
	}
	policy := storage.MLRetrainPolicy{
		Schedule: strings.TrimSpace(req.Schedule), NewRows: req.NewRows,
		Spec: req.Spec, GoLive: storage.MLGoLive(req.GoLive),
	}
	if err := ml.ValidateRetrainPolicy(policy); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := r.PathValue("name")
	m, err := h.service().SetRetrainPolicy(r.Context(), vhost, name, policy, user.Username)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Set the retrain policy of model "+name+" in vhost "+vhost, "update", vhost, "vhost", "",
		map[string]any{"model": name, "retrain_schedule": policy.Schedule, "retrain_new_rows": policy.NewRows, "dataset": policy.Spec.Dataset})
	writeJSON(w, m)
}

// DeleteRetrain stops a model retraining by itself.
func (h *Handler) DeleteRetrain(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if err := h.service().ClearRetrainPolicy(r.Context(), vhost, name); err != nil {
		h.fail(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Cleared the retrain policy of model "+name+" in vhost "+vhost, "update", vhost, "vhost", "",
		map[string]string{"model": name})
	w.WriteHeader(http.StatusNoContent)
}
