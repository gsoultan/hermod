package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
)

// Recommendation scenarios: answers, feedback, dataset refreshes and builds
// (docs/ml.md, "Recommendation scenarios").

// RecommendScenario answers a scenario call from the UI, as an Editor of the
// model's vhost.
func (h *Handler) RecommendScenario(w http.ResponseWriter, r *http.Request) {
	vhost, user, ok := h.access(w, r, true)
	if !ok {
		return
	}
	h.recommendScenario(w, r.WithContext(ml.WithCaller(r.Context(), storage.MLCallerUI, "")), vhost, r.PathValue("name"),
		storage.MLCallerUI, user.Username)
}

// ServeRecommend is RecommendScenario for an application, with the model's
// serving key, refused as Serve refuses.
func (h *Handler) ServeRecommend(w http.ResponseWriter, r *http.Request) {
	vhost, name := r.PathValue("vhost"), r.PathValue("name")
	key := r.Header.Get("X-API-Key")
	if key == "" {
		key = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	if key == "" || h.service().AuthorizeServing(r.Context(), vhost, name, key) != nil {
		h.JsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	h.recommendScenario(w, r.WithContext(ml.WithCaller(r.Context(), storage.MLCallerREST, "")), vhost, name, storage.MLCallerREST, "")
}

// recommendScenario answers a scenario call as predict answers its calls: at
// once, within the sync time limit, or as a job when the caller asks for one
// with "respond": "async" or Prefer: respond-async. callerKind and by say who
// is calling, for a job.
func (h *Handler) recommendScenario(w http.ResponseWriter, r *http.Request, vhost, name, callerKind, by string) {
	var req ml.ScenarioRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPredictBody))
	if err := dec.Decode(&req); err != nil {
		h.JsonError(w, `the request body must be JSON: {"for": {"customer": "..."}, "k": 10}`, http.StatusBadRequest)
		return
	}
	async, err := asyncWanted(r, req.Respond)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !async && req.CallbackURL != "" {
		h.JsonError(w, `"callback_url" applies to a call sent with "respond": "async"`, http.StatusBadRequest)
		return
	}
	callback := req.CallbackURL
	req.Respond, req.CallbackURL = "", ""
	if async {
		h.submitJob(w, r, ml.JobSubmission{VHost: vhost, Model: name, Kind: storage.MLJobRecommend, Scenario: &req,
			CallbackURL: callback, CallerKind: callerKind, CreatedBy: by})
		return
	}
	svc := h.service()
	var out ml.ScenarioAnswer
	err = svc.Sync(r.Context(), vhost, name, func(ctx context.Context) error {
		var err error
		out, err = svc.RecommendScenario(ctx, vhost, name, req)
		return err
	})
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, out)
}

// PostFeedback records what happened to the items of one answer.
func (h *Handler) PostFeedback(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	var req ml.FeedbackRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPredictBody)).Decode(&req); err != nil {
		h.JsonError(w, `the request body must be JSON: {"recommendation_id": "...", "events": [{"id", "outcome"}]}`, http.StatusBadRequest)
		return
	}
	if err := h.service().RecordFeedback(r.Context(), vhost, r.PathValue("name"), req); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetFeedbackSummary counts a model's feedback over ?days= (30 by default).
func (h *Handler) GetFeedbackSummary(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, false)
	if !ok {
		return
	}
	days := 0
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			h.JsonError(w, "days is a whole number of days", http.StatusBadRequest)
			return
		}
		days = n
	}
	sum, err := h.service().FeedbackSummary(r.Context(), vhost, r.PathValue("name"), days)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, sum)
}

// PutDatasetRefresh makes a dataset refill itself from a database query on a
// schedule.
func (h *Handler) PutDatasetRefresh(w http.ResponseWriter, r *http.Request) {
	vhost, user, ok := h.access(w, r, true)
	if !ok {
		return
	}
	var req ml.DatasetRefresh
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxModelBody)).Decode(&req); err != nil {
		h.JsonError(w, `the request body must be JSON: {"schedule", "source_id", "query", "max_rows"}`, http.StatusBadRequest)
		return
	}
	name := r.PathValue("name")
	st, err := h.service().SetDatasetRefresh(r.Context(), vhost, name, req, user.Username)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Scheduled a refresh of dataset "+name+" in vhost "+vhost, "update", vhost, "vhost", "",
		map[string]any{"dataset": name, "schedule": req.Schedule, "source_id": req.SourceID})
	writeJSON(w, st)
}

// DeleteDatasetRefresh stops a dataset refreshing itself.
func (h *Handler) DeleteDatasetRefresh(w http.ResponseWriter, r *http.Request) {
	h.deleteSchedule(w, r, storage.MLScheduleDatasetRefresh, "dataset")
}

// PutModelBuild makes a recommendation model build its answers for every
// salesman, customer or product on a schedule, into a table.
func (h *Handler) PutModelBuild(w http.ResponseWriter, r *http.Request) {
	vhost, user, ok := h.access(w, r, true)
	if !ok {
		return
	}
	var req ml.ModelBuild
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxModelBody)).Decode(&req); err != nil {
		h.JsonError(w, `the request body must be JSON: {"schedule", "for", "period_days", "within_days", "k", "target": {"source_id", "table", "mode"}}`,
			http.StatusBadRequest)
		return
	}
	name := r.PathValue("name")
	st, err := h.service().SetModelBuild(r.Context(), vhost, name, req, user.Username)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Scheduled a build of model "+name+" in vhost "+vhost, "update", vhost, "vhost", "",
		map[string]any{"model": name, "schedule": req.Schedule, "for": req.For, "source_id": req.Target.SourceID, "table": req.Target.Table})
	writeJSON(w, st)
}

// GetModelBuild shows a model's scheduled build and its status: 404 for
// none.
func (h *Handler) GetModelBuild(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, false)
	if !ok {
		return
	}
	sc, st, err := h.service().Schedule(r.Context(), vhost, storage.MLScheduleModelBuild, r.PathValue("name"))
	if err != nil {
		h.fail(w, err)
		return
	}
	if sc == nil {
		h.JsonError(w, "this model has no scheduled build", http.StatusNotFound)
		return
	}
	var spec ml.ModelBuild
	_ = json.Unmarshal(sc.Spec, &spec)
	writeJSON(w, map[string]any{"build": spec, "status": st})
}

// DeleteModelBuild stops a model's scheduled build.
func (h *Handler) DeleteModelBuild(w http.ResponseWriter, r *http.Request) {
	h.deleteSchedule(w, r, storage.MLScheduleModelBuild, "model")
}

func (h *Handler) deleteSchedule(w http.ResponseWriter, r *http.Request, kind, what string) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	name := r.PathValue("name")
	svc := h.service()
	var err error
	if kind == storage.MLScheduleDatasetRefresh {
		err = svc.ClearDatasetRefresh(r.Context(), vhost, name)
	} else {
		err = svc.ClearModelBuild(r.Context(), vhost, name)
	}
	if errors.Is(err, storage.ErrNotFound) {
		h.JsonError(w, "this "+what+" has no schedule", http.StatusNotFound)
		return
	}
	if err != nil {
		h.fail(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Removed the "+kind+" schedule of "+name+" in vhost "+vhost, "update", vhost, "vhost", "",
		map[string]string{what: name})
	w.WriteHeader(http.StatusNoContent)
}

// failRecommend maps the scenario errors onto a status; false for anything
// else.
func (h *Handler) failRecommend(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, ml.ErrBadFeedback), errors.Is(err, ml.ErrBadSchedule):
		h.JsonError(w, err.Error(), http.StatusBadRequest)
	default:
		return false
	}
	return true
}
