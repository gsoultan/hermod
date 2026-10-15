package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/builtin"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// maxDatasetFile bounds an uploaded CSV, Excel or Parquet dataset. It is streamed to
// the worker, never held here; the worker applies its own limit too.
const maxDatasetFile = 200 << 20

// WorkerStatus says whether training is available: an engine is in force
// (Hermod's built-in one, a hermod-ml worker, or both), and it answers. Any
// signed-in user may ask; the UI uses it to show or hide training, and
// offers only the algorithms listed here when the engines say. A worker that
// cannot say (one older than its capabilities route) leaves the list out.
// "engines" reports each engine: the built-in one, and the worker with
// whether it answers, so a worker that is down is seen while the built-in
// engine goes on training.
func (h *Handler) WorkerStatus(w http.ResponseWriter, r *http.Request) {
	svc := h.service()
	wk, err := svc.Worker()
	if err != nil {
		writeJSON(w, map[string]any{"configured": false, "ready": false, "source": worker.SourceNone, "capabilities": svc.Capabilities(),
			"engines": map[string]any{"builtin": builtinStatus(nil), "worker": map[string]any{"configured": false, "ready": false}}})
		return
	}
	status := map[string]any{"configured": true, "ready": true, "source": h.workerSource(wk), "capabilities": svc.Capabilities(),
		"engines": map[string]any{"builtin": builtinStatus(wk), "worker": h.remoteStatus(r.Context(), wk.Remote())}}
	if err := wk.Ready(r.Context()); err != nil {
		status["ready"] = false
		status["error"] = "the ML worker does not answer"
		writeJSON(w, status)
		return
	}
	if caps, err := wk.Capabilities(r.Context()); err == nil {
		status["algorithms"] = caps.Algorithms
		status["unavailable"] = caps.Unavailable
	}
	writeJSON(w, status)
}

// builtinStatus reports Hermod's built-in engine: available when it is in
// force, with what it trains by itself.
func builtinStatus(wk *worker.Client) map[string]any {
	if wk == nil || wk.Local() == nil {
		return map[string]any{"available": false}
	}
	caps := builtin.Capabilities()
	return map[string]any{"available": true, "algorithms": caps.Algorithms}
}

// remoteStatus reports the hermod-ml worker: whether one is connected, where
// it was configured, whether it answers, and what it trains.
func (h *Handler) remoteStatus(ctx context.Context, remote *worker.Client) map[string]any {
	if remote == nil {
		return map[string]any{"configured": false, "ready": false}
	}
	st := map[string]any{"configured": true, "ready": true, "source": h.remoteSource(remote)}
	if err := remote.Ready(ctx); err != nil {
		st["ready"], st["error"] = false, "the ML worker does not answer"
		return st
	}
	if caps, err := remote.Capabilities(ctx); err == nil {
		st["algorithms"], st["unavailable"] = caps.Algorithms, caps.Unavailable
	}
	return st
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

// GetDataset shows a dataset with a sample of its rows, and its scheduled
// refresh ("refresh": null for none). Rows are data, so reading them needs
// the Editor role, as reading a workflow's samples does.
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
	_, refresh, err := h.service().Schedule(r.Context(), vhost, storage.MLScheduleDatasetRefresh, r.PathValue("name"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, struct {
		worker.DatasetInfo
		Refresh *ml.ScheduleStatus `json:"refresh"`
	}{info, refresh})
}

// UploadDataset replaces a dataset with a CSV, Excel or Parquet file sent as
// the body.
func (h *Handler) UploadDataset(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	format := r.URL.Query().Get("format")
	if !slices.Contains(worker.FileFormats, format) {
		h.JsonError(w, "a dataset file is CSV (format=csv), Excel .xlsx (format=xlsx) or Parquet (format=parquet)", http.StatusBadRequest)
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

// ProfileDataset analyzes a dataset (pkg/ml/profile): its columns, its
// problems with how to fix them, and the numeric columns that move together,
// computed on a sample of a big one. The profile quotes values, so it needs
// the Editor role, as reading the rows does.
func (h *Handler) ProfileDataset(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	p, err := h.service().ProfileDataset(r.Context(), vhost, r.PathValue("name"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, p)
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

// namesItsData reports whether a training body names what it learns from: a
// dataset and a target, a recommendation's dataset, or a scenario's data
// roles (whose transactions dataset is the training's dataset).
func namesItsData(spec worker.TrainSpec) bool {
	if spec.Task == worker.TaskRecommendation {
		return spec.Dataset != "" || len(spec.Data) > 0
	}
	return spec.Dataset != "" && spec.Target != ""
}

// RecommendCheck is a recommendation scenario's data health check: the
// train body without name and rules; it trains nothing. A body that is not a
// recommendation scenario's is refused with 400. Like training, it reads
// rows, so it needs the Editor role.
func (h *Handler) RecommendCheck(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	var req trainBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxModelBody)).Decode(&req); err != nil {
		h.JsonError(w, "the request body must be a JSON training spec", http.StatusBadRequest)
		return
	}
	res, err := h.service().RecommendCheck(r.Context(), vhost, req.TrainSpec)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, res)
}

// trainBody is a training asked for from the UI or the API. "respond":
// "async" queues it as a job, and "callback_url" is told when it ends.
type trainBody struct {
	worker.TrainSpec
	GoLive      ml.GoLive `json:"go_live"`
	Respond     string    `json:"respond"`
	CallbackURL string    `json:"callback_url"`
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
	// A recommendation model has no target: its columns are in recommend,
	// or a scenario's in data, which the service checks.
	if !namesItsData(req.TrainSpec) {
		h.JsonError(w, "name the dataset to train on and the target column to predict", http.StatusBadRequest)
		return
	}
	if err := req.GoLive.Validate(); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := r.PathValue("name")
	async, err := asyncWanted(r, req.Respond)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if async {
		// The job trains as the user who queued it; the audit records the
		// queueing, and the job holds what the training did.
		j, ok := h.submitJob(w, r, ml.JobSubmission{VHost: vhost, Model: name, Kind: storage.MLJobTrain,
			Train: &ml.TrainJob{Spec: req.TrainSpec, GoLive: req.GoLive}, CallbackURL: req.CallbackURL,
			CallerKind: storage.MLCallerUI, CreatedBy: user.Username})
		if ok {
			h.RecordAuditLog(r, "INFO", "Queued training of model "+name+" in vhost "+vhost, "update", vhost, "vhost", "",
				map[string]any{"model": name, "job": j.ID, "dataset": req.Dataset, "device": req.Device})
		}
		return
	}
	if req.CallbackURL != "" {
		h.JsonError(w, `"callback_url" applies to a training sent with "respond": "async"`, http.StatusBadRequest)
		return
	}
	res, err := h.service().Train(r.Context(), vhost, name, req.TrainSpec, req.GoLive, user.Username)
	if err != nil {
		h.fail(w, err)
		return
	}
	details := map[string]any{"model": name, "trained_version": res.Version.Version, "live": res.Live, "dataset": req.Dataset,
		"algorithm": res.Version.Algorithm, "device": req.Device}
	if res.Version.Script != nil {
		details["script"], details["script_sha256"] = res.Version.Script.Name, res.Version.Script.SHA256
	}
	h.RecordAuditLog(r, "INFO", "Trained model "+name+" in vhost "+vhost, "update", vhost, "vhost", "", details)
	writeJSON(w, res)
}

// TrainCheck is the pre-flight check before a training: it takes the train
// body, trains nothing, and says what the worker makes of the dataset. The
// model name, go_live and params are ignored. Like training, it reads rows,
// so it needs the Editor role.
func (h *Handler) TrainCheck(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	var req trainBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxModelBody)).Decode(&req); err != nil {
		h.JsonError(w, "the request body must be a JSON training spec", http.StatusBadRequest)
		return
	}
	// A recommendation body has no target; the service says why it is refused.
	if !namesItsData(req.TrainSpec) {
		h.JsonError(w, "name the dataset to train on and the target column to predict", http.StatusBadRequest)
		return
	}
	spec := req.TrainSpec
	spec.Params = nil
	res, err := h.service().TrainCheck(r.Context(), vhost, spec)
	if err != nil {
		h.fail(w, err)
		return
	}
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
