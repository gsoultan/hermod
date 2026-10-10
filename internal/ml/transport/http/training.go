package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// maxDatasetFile bounds an uploaded CSV or Excel dataset. It is streamed to
// the worker, never held here; the worker applies its own limit too.
const maxDatasetFile = 200 << 20

// WorkerStatus says whether training is available: a worker is configured,
// and it answers. Any signed-in user may ask; the UI uses it to show or hide
// training.
func (h *Handler) WorkerStatus(w http.ResponseWriter, r *http.Request) {
	wk, err := h.service().Worker()
	if err != nil {
		writeJSON(w, map[string]any{"configured": false, "ready": false})
		return
	}
	status := map[string]any{"configured": true, "ready": true}
	if err := wk.Ready(r.Context()); err != nil {
		status["ready"] = false
		status["error"] = "the ML worker does not answer"
	}
	writeJSON(w, status)
}

func (h *Handler) ListDatasets(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, false)
	if !ok {
		return
	}
	wk, err := h.service().Worker()
	if err != nil {
		h.fail(w, err)
		return
	}
	list, err := wk.Datasets(r.Context(), vhost)
	if err != nil {
		h.fail(w, err)
		return
	}
	if list == nil {
		list = []worker.DatasetInfo{}
	}
	writeJSON(w, map[string]any{"data": list, "total": len(list)})
}

// GetDataset shows a dataset with a sample of its rows. Rows are data, so
// reading them needs the Editor role, as reading a workflow's samples does.
func (h *Handler) GetDataset(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	wk, err := h.service().Worker()
	if err != nil {
		h.fail(w, err)
		return
	}
	info, err := wk.Dataset(r.Context(), vhost, r.PathValue("name"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, info)
}

// UploadDataset replaces a dataset with a CSV or Excel file sent as the body.
func (h *Handler) UploadDataset(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	format := r.URL.Query().Get("format")
	if format != "csv" && format != "xlsx" {
		h.JsonError(w, "a dataset file is CSV (format=csv) or Excel .xlsx (format=xlsx)", http.StatusBadRequest)
		return
	}
	name := r.PathValue("name")
	info, err := h.service().UploadDataset(r.Context(), vhost, name, format, http.MaxBytesReader(w, r.Body, maxDatasetFile))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			h.JsonError(w, "the file is larger than 200 MB", http.StatusRequestEntityTooLarge)
			return
		}
		h.fail(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Uploaded dataset "+name+" in vhost "+vhost, "update", vhost, "vhost", "",
		map[string]any{"dataset": name, "format": format, "rows": info.Rows})
	writeJSON(w, info)
}

// queryRequest fills a dataset from one of the vhost's database sources.
type queryRequest struct {
	SourceID string `json:"source_id"`
	Query    string `json:"query"`
	MaxRows  int    `json:"max_rows"`
}

func (h *Handler) DatasetFromQuery(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	var req queryRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxModelBody)).Decode(&req); err != nil ||
		req.SourceID == "" || strings.TrimSpace(req.Query) == "" {
		h.JsonError(w, `the body must name a database source and a query: {"source_id": "...", "query": "SELECT ..."}`, http.StatusBadRequest)
		return
	}
	if h.Registry == nil {
		h.JsonError(w, "reading from a database needs the engine, which is not running yet", http.StatusServiceUnavailable)
		return
	}
	name := r.PathValue("name")
	n, err := h.Registry.MLDatasetFromQuery(r.Context(), vhost, name, req.SourceID, req.Query, req.MaxRows)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Filled dataset "+name+" in vhost "+vhost+" from a query", "update", vhost, "vhost", "",
		map[string]any{"dataset": name, "source_id": req.SourceID, "rows": n})
	writeJSON(w, map[string]any{"name": name, "rows": n})
}

func (h *Handler) DeleteDataset(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	wk, err := h.service().Worker()
	if err != nil {
		h.fail(w, err)
		return
	}
	name := r.PathValue("name")
	if err := wk.DeleteDataset(r.Context(), vhost, name); err != nil {
		h.fail(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Deleted dataset "+name+" in vhost "+vhost, "delete", vhost, "vhost", "",
		map[string]string{"dataset": name})
	w.WriteHeader(http.StatusNoContent)
}

// trainBody is a training asked for from the UI or the API.
type trainBody struct {
	worker.TrainSpec
	GoLive ml.GoLive `json:"go_live"`
}

func (h *Handler) TrainModel(w http.ResponseWriter, r *http.Request) {
	vhost, user, ok := h.access(w, r, true)
	if !ok {
		return
	}
	var req trainBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxModelBody)).Decode(&req); err != nil {
		h.JsonError(w, "the request body must be a JSON training spec", http.StatusBadRequest)
		return
	}
	if req.Dataset == "" || req.Target == "" {
		h.JsonError(w, "name the dataset to train on and the target column to predict", http.StatusBadRequest)
		return
	}
	if err := req.GoLive.Validate(); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := r.PathValue("name")
	res, err := h.service().Train(r.Context(), vhost, name, req.TrainSpec, req.GoLive, user.Username)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Trained model "+name+" in vhost "+vhost, "update", vhost, "vhost", "",
		map[string]any{"model": name, "trained_version": res.Version.Version, "live": res.Live, "dataset": req.Dataset})
	writeJSON(w, res)
}

func (h *Handler) ListVersions(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, false)
	if !ok {
		return
	}
	vs, err := h.service().Versions(r.Context(), vhost, r.PathValue("name"))
	if err != nil {
		h.fail(w, err)
		return
	}
	if vs == nil {
		vs = []worker.Version{}
	}
	writeJSON(w, map[string]any{"data": vs, "total": len(vs)})
}

// PromoteVersion puts one version of a trained model live.
func (h *Handler) PromoteVersion(w http.ResponseWriter, r *http.Request) {
	vhost, user, ok := h.access(w, r, true)
	if !ok {
		return
	}
	name, version := r.PathValue("name"), r.PathValue("version")
	if err := h.service().Promote(r.Context(), vhost, name, version, user.Username); err != nil {
		h.fail(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Put version "+version+" of model "+name+" live in vhost "+vhost, "update", vhost, "vhost", "",
		map[string]string{"model": name, "live_version": version})
	m, err := h.service().Model(r.Context(), vhost, name)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, m)
}
