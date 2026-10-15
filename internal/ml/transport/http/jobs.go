// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
)

// How a caller asks for a call to be answered: "sync" waits for the answer,
// "async" queues a job and answers 202 with where to read it. Empty is sync,
// unless the request carries "Prefer: respond-async" (RFC 7240).
const (
	respondSync  = "sync"
	respondAsync = "async"
)

// defaultJobPage is a job listing's page size when the caller names none.
const defaultJobPage = 50

// errBadRespond is the refusal of a "respond" that is neither.
var errBadRespond = errors.New(`"respond" is "sync" or "async"`)

// asyncWanted reports whether a call is to be answered as a job: its body's
// "respond", or else a Prefer header asking for respond-async.
func asyncWanted(r *http.Request, respond string) (bool, error) {
	switch respond {
	case respondAsync:
		return true, nil
	case respondSync:
		return false, nil
	case "":
	default:
		return false, errBadRespond
	}
	for _, v := range r.Header.Values("Prefer") {
		for pref := range strings.SplitSeq(v, ",") {
			name, _, _ := strings.Cut(pref, "=")
			if strings.EqualFold(strings.TrimSpace(name), "respond-async") {
				return true, nil
			}
		}
	}
	return false, nil
}

// jobView is a job as the API shows it: where to read it and, when its
// result is kept apart, where to read that. A prediction job's rows are left
// out; the caller sent them.
type jobView struct {
	storage.MLJob
	StatusURL string `json:"status_url"`
	ResultURL string `json:"result_url,omitempty"`
}

func viewOf(j storage.MLJob, statusURL string) jobView {
	if j.Kind != storage.MLJobTrain {
		j.Request = nil
	}
	v := jobView{MLJob: j, StatusURL: statusURL}
	if j.ResultRef == storage.MLJobResultStored {
		v.ResultURL = statusURL + "/result"
	}
	return v
}

// submitJob queues a job and answers 202 with its id and where to read it.
func (h *Handler) submitJob(w http.ResponseWriter, r *http.Request, sub ml.JobSubmission) (storage.MLJob, bool) {
	j, err := h.service().SubmitJob(r.Context(), sub)
	if err != nil {
		h.fail(w, err)
		return storage.MLJob{}, false
	}
	statusURL := ml.JobStatusPath(j)
	w.Header().Set("Location", statusURL)
	w.Header().Set("Preference-Applied", "respond-async")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"job_id": j.ID, "status": j.Status, "status_url": statusURL})
	return j, true
}

// ListJobs lists a vhost's jobs, newest first, without their requests or
// results: any role on the vhost may see what is running. ?model= and
// ?status= filter; ?limit= and ?offset= page.
func (h *Handler) ListJobs(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, false)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = defaultJobPage
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	list, total, err := h.service().ListJobs(r.Context(), storage.MLJobFilter{
		VHost: vhost, Model: q.Get("model"), Status: q.Get("status"),
		Limit: min(limit, storage.MaxMLJobPage), Offset: max(offset, 0)})
	if err != nil {
		h.fail(w, err)
		return
	}
	out := make([]jobView, len(list))
	for i, j := range list {
		out[i] = viewOf(j, ml.JobStatusPath(storage.MLJob{VHost: vhost, ID: j.ID}))
	}
	writeJSON(w, map[string]any{"data": out, "total": total})
}

// GetJob shows one job with its result. The result is the model's output, so
// it needs the Editor role, as calling the model does.
func (h *Handler) GetJob(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	j, err := h.service().Job(r.Context(), vhost, r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, viewOf(j, ml.JobStatusPath(storage.MLJob{VHost: vhost, ID: j.ID})))
}

// GetJobResult answers a job's result on its own: the way to read one too
// large to be kept on the job.
func (h *Handler) GetJobResult(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	h.writeResult(w, r, vhost, r.PathValue("id"))
}

func (h *Handler) writeResult(w http.ResponseWriter, r *http.Request, vhost, id string) {
	raw, err := h.service().JobResult(r.Context(), vhost, id)
	if err != nil {
		h.fail(w, err)
		return
	}
	if raw == nil {
		h.JsonError(w, "this job has no result: it has not succeeded", http.StatusConflict)
		return
	}
	writeJSON(w, raw)
}

// CancelJob cancels a queued job, or stops a running one within seconds,
// and returns the job.
func (h *Handler) CancelJob(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	j, err := h.service().CancelJob(r.Context(), vhost, r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Cancelled ML job "+j.ID+" of model "+j.Model+" in vhost "+vhost, "update", vhost, "vhost", "",
		map[string]string{"model": j.Model, "job": j.ID, "kind": j.Kind})
	writeJSON(w, viewOf(j, ml.JobStatusPath(storage.MLJob{VHost: vhost, ID: j.ID})))
}

// servingKey is the key an application presents: X-API-Key or a bearer
// token.
func servingKey(r *http.Request) string {
	if key := r.Header.Get("X-API-Key"); key != "" {
		return key
	}
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

// servingJob is the job an application asks about, when its key may read
// it: a job submitted with a serving key, for the model the key is for.
// Every refusal but a missing key is the same 404, so a key does not learn
// of jobs that are not its own.
func (h *Handler) servingJob(w http.ResponseWriter, r *http.Request) (*ml.Service, storage.MLJob, bool) {
	key := servingKey(r)
	if key == "" {
		h.JsonError(w, "Unauthorized", http.StatusUnauthorized)
		return nil, storage.MLJob{}, false
	}
	svc := h.service()
	vhost := r.PathValue("vhost")
	j, err := svc.Job(r.Context(), vhost, r.PathValue("id"))
	if err != nil || !ml.ServingJob(j, j.Model) || svc.AuthorizeServing(r.Context(), vhost, j.Model, key) != nil {
		h.JsonError(w, "no such job", http.StatusNotFound)
		return nil, storage.MLJob{}, false
	}
	return svc, j, true
}

// ServeJob is GetJob for an application, with the serving key of the job's
// model.
func (h *Handler) ServeJob(w http.ResponseWriter, r *http.Request) {
	_, j, ok := h.servingJob(w, r)
	if !ok {
		return
	}
	writeJSON(w, viewOf(j, ml.JobStatusPath(j)))
}

// ServeJobResult is GetJobResult for an application.
func (h *Handler) ServeJobResult(w http.ResponseWriter, r *http.Request) {
	_, j, ok := h.servingJob(w, r)
	if !ok {
		return
	}
	h.writeResult(w, r, j.VHost, j.ID)
}

// ServeCancelJob is CancelJob for an application.
func (h *Handler) ServeCancelJob(w http.ResponseWriter, r *http.Request) {
	svc, j, ok := h.servingJob(w, r)
	if !ok {
		return
	}
	j, err := svc.CancelJob(r.Context(), j.VHost, j.ID)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, viewOf(j, ml.JobStatusPath(j)))
}
