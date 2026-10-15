// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"

	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// Route returns a client of the engine and the hermod-ml worker remote
// together, as Hermod runs them: connecting or disconnecting the worker never
// changes which engine holds a model's versions.
//
//   - A version is served by the engine that trained it, which its id says
//     (worker.IsBuiltinVersion).
//   - A training with a built-in algorithm runs in-process; one only the
//     worker trains, and auto, run on the worker.
//   - Datasets are kept in the engine's store and copied to the worker when
//     it trains on them; one only the worker has (from before the engine
//     kept datasets) stays there and is listed beside the engine's.
//
// With no worker it is the engine alone.
func (e *Engine) Route(remote *worker.Client) *worker.Client {
	if remote == nil {
		return e.Client()
	}
	rt := &router{e: e, remote: remote}
	rt.routes()
	return worker.NewLocal(rt)
}

// router answers the worker's routes from the engine and the worker.
type router struct {
	e      *Engine
	remote *worker.Client
	mux    *http.ServeMux
}

// Remote is the worker the router reaches.
func (rt *router) Remote() *worker.Client { return rt.remote }

func (rt *router) ServeHTTP(w http.ResponseWriter, r *http.Request) { rt.mux.ServeHTTP(w, r) }

// Predict scores rows with a version the engine trained. The worker's
// versions are scored on the worker, through the Open Inference Protocol;
// the engine refuses them.
func (rt *router) Predict(ctx context.Context, vhost, model, version string, rows []map[string]any) ([]map[string]any, error) {
	return rt.e.Predict(ctx, vhost, model, version, rows)
}

func (rt *router) routes() {
	m := http.NewServeMux()
	m.HandleFunc("GET /v1/capabilities", rt.e.handle(rt.capabilities))

	m.HandleFunc("POST /v1/datasets/{vhost}/{name}/rows", rt.onEngineOrWorker)
	m.HandleFunc("GET /v1/datasets/{vhost}", rt.e.handle(rt.datasets))
	m.HandleFunc("GET /v1/datasets/{vhost}/{name}", rt.e.handle(rt.dataset))
	m.HandleFunc("DELETE /v1/datasets/{vhost}/{name}", rt.e.handle(rt.deleteDataset))
	m.HandleFunc("GET /v1/datasets/{vhost}/{name}/sample", rt.onEngineOrWorker)
	m.HandleFunc("GET /v1/datasets/{vhost}/{name}/export", rt.exportDataset)
	m.HandleFunc("PUT /v1/datasets/{vhost}/{name}/import", rt.remote.Forward)

	m.HandleFunc("POST /v1/models/{vhost}/{name}/train", rt.train)
	m.HandleFunc("POST /v1/train-check/{vhost}", rt.train)
	m.HandleFunc("POST /v1/recommend-check/{vhost}", rt.onWorker)
	m.HandleFunc("POST /v1/models/{vhost}/{name}/train-custom", rt.onWorker)
	m.HandleFunc("POST /v1/models/{vhost}/{name}/import", rt.onWorker)
	m.HandleFunc("GET /v1/models/{vhost}/{name}/versions", rt.e.handle(rt.versions))
	m.HandleFunc("GET /v1/models/{vhost}/{name}/versions/{version}/{file}", rt.versionFile)
	m.HandleFunc("DELETE /v1/models/{vhost}/{name}", rt.e.handle(rt.deleteModel))
	m.Handle("/", rt.e)
	rt.mux = m
}

// capabilities is what either engine trains. What neither trains is
// unavailable, with the worker's reason when it gives one.
func (rt *router) capabilities(r *http.Request) (any, error) {
	caps := capabilities()
	theirs, err := rt.remote.Capabilities(r.Context())
	if err != nil {
		slog.Debug("ml: the hermod-ml worker did not say what it trains", "error", err)
		theirs = worker.Capabilities{}
	}
	for _, t := range theirs.Tasks {
		if !slices.Contains(caps.Tasks, t) {
			caps.Tasks = append(caps.Tasks, t)
		}
	}
	for _, a := range theirs.Algorithms {
		if !slices.Contains(caps.Algorithms, a) {
			caps.Algorithms = append(caps.Algorithms, a)
		}
	}
	maps := []map[string]string{caps.Unavailable, theirs.Unavailable}
	caps.Unavailable = map[string]string{}
	for _, m := range maps {
		for a, why := range m {
			if !slices.Contains(caps.Algorithms, a) {
				caps.Unavailable[a] = why
			}
		}
	}
	return caps, nil
}

// remoteError is a worker's refusal as the engine answers one, with the
// worker's words.
func remoteError(err error) error {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, worker.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, worker.ErrBusy):
		status = http.StatusTooManyRequests
	case errors.Is(err, worker.ErrUnauthorized):
		status = http.StatusUnauthorized
	}
	return &apiError{status: status, msg: err.Error()}
}

// onlyOnWorker reports whether the dataset is one the worker has and the
// engine does not.
func (rt *router) onlyOnWorker(ctx context.Context, vhost, name string) bool {
	if has, err := rt.e.datasets.has(ctx, vhost, name); err != nil || has {
		return false
	}
	_, err := rt.remote.Dataset(ctx, vhost, name)
	return err == nil
}

// onEngineOrWorker answers a request about a dataset (adding rows to it,
// or a sample of it) from the engine, or from the worker when only the worker
// has the dataset.
func (rt *router) onEngineOrWorker(w http.ResponseWriter, r *http.Request) {
	if rt.onlyOnWorker(r.Context(), r.PathValue("vhost"), r.PathValue("name")) {
		rt.remote.Forward(w, r)
		return
	}
	rt.e.ServeHTTP(w, r)
}

// datasets lists both engines' datasets; the engine's copy of one both have
// is the one listed.
func (rt *router) datasets(r *http.Request) (any, error) {
	ctx, vhost := r.Context(), r.PathValue("vhost")
	list, err := rt.e.datasets.list(ctx, vhost)
	if err != nil {
		return nil, err
	}
	theirs, err := rt.remote.Datasets(ctx, vhost)
	if err != nil {
		slog.Debug("ml: the hermod-ml worker did not list its datasets", "vhost", vhost, "error", err)
	}
	for _, d := range theirs {
		if !slices.ContainsFunc(list, func(o worker.DatasetInfo) bool { return o.Name == d.Name }) {
			d.Engine = worker.EngineWorker
			list = append(list, d)
		}
	}
	slices.SortFunc(list, func(a, b worker.DatasetInfo) int { return strings.Compare(a.Name, b.Name) })
	return map[string]any{"datasets": list}, nil
}

// dataset describes the engine's dataset, else the worker's.
func (rt *router) dataset(r *http.Request) (any, error) {
	info, err := rt.e.getDataset(r)
	var ae *apiError
	if err == nil || !errors.As(err, &ae) || ae.status != http.StatusNotFound {
		return info, err
	}
	theirs, rerr := rt.remote.Dataset(r.Context(), r.PathValue("vhost"), r.PathValue("name"))
	if rerr != nil {
		return nil, err
	}
	theirs.Engine = worker.EngineWorker
	return theirs, nil
}

// deleteDataset removes the dataset from both engines.
func (rt *router) deleteDataset(r *http.Request) (any, error) {
	ctx, vhost, name := r.Context(), r.PathValue("vhost"), r.PathValue("name")
	err := rt.e.datasets.delete(ctx, vhost, name)
	rt.e.forgetSync(rt.remote, vhost, name)
	rerr := rt.remote.DeleteDataset(ctx, vhost, name)
	return nil, bothDeleted("dataset", err, rerr)
}

// bothDeleted is the answer to removing something from both engines: done
// when either had it, not found when neither did. A worker that cannot be
// reached is an error only when the engine had nothing to remove.
func bothDeleted(what string, local, remote error) error {
	var ae *apiError
	localMissing := local != nil && errors.As(local, &ae) && ae.status == http.StatusNotFound
	switch {
	case local != nil && !localMissing:
		return local
	case remote == nil:
		return nil
	case !localMissing:
		if !errors.Is(remote, worker.ErrNotFound) {
			slog.Warn("ml: could not remove a "+what+" from the hermod-ml worker", "error", remote)
		}
		return nil
	case errors.Is(remote, worker.ErrNotFound):
		return local
	}
	return remoteError(remote)
}

// exportDataset hands the worker's copy of the dataset on, copying the
// engine's to it first.
func (rt *router) exportDataset(w http.ResponseWriter, r *http.Request) {
	if err := rt.e.syncDataset(r.Context(), rt.remote, r.PathValue("vhost"), r.PathValue("name")); err != nil {
		fail(w, err)
		return
	}
	rt.remote.Forward(w, r)
}

// train runs a training, or its pre-flight check, where its algorithm runs:
// in-process for a built-in one, else on the worker, which gets the engine's
// datasets the training reads first.
func (rt *router) train(w http.ResponseWriter, r *http.Request) {
	raw, err := rt.e.body(r)
	if err != nil {
		fail(w, err)
		return
	}
	var spec worker.TrainSpec
	if json.Unmarshal(raw, &spec) != nil || !rt.trainsOnWorker(r.Context(), r.PathValue("vhost"), spec) {
		rt.e.ServeHTTP(w, withBody(r, raw))
		return
	}
	rt.toWorker(w, withBody(r, raw), spec)
}

// trainsOnWorker reports whether spec trains on the worker: an algorithm
// only the worker trains, auto, a recommendation scenario, or a dataset
// only the worker has.
func (rt *router) trainsOnWorker(ctx context.Context, vhost string, spec worker.TrainSpec) bool {
	switch {
	case spec.Data != nil || (spec.Scenario != "" && spec.Scenario != "products"):
		return true
	case spec.Algorithm == "" || spec.Algorithm == "auto":
		return true
	case !slices.Contains(algorithms, spec.Algorithm):
		return true
	}
	return spec.Dataset != "" && rt.onlyOnWorker(ctx, vhost, spec.Dataset)
}

// onWorker sends a request only the worker answers (a recommendation check,
// a custom script, an imported version) on to it, with the engine's datasets
// it names copied to it first.
func (rt *router) onWorker(w http.ResponseWriter, r *http.Request) {
	raw, err := rt.e.body(r)
	if err != nil {
		fail(w, err)
		return
	}
	var spec worker.TrainSpec
	_ = json.Unmarshal(raw, &spec)
	rt.toWorker(w, withBody(r, raw), spec)
}

// toWorker copies the datasets spec reads to the worker and sends r on to
// it. A version it answers is marked as the worker's.
func (rt *router) toWorker(w http.ResponseWriter, r *http.Request, spec worker.TrainSpec) {
	vhost := r.PathValue("vhost")
	for _, name := range specDatasets(spec) {
		if err := rt.e.syncDataset(r.Context(), rt.remote, vhost, name); err != nil {
			fail(w, err)
			return
		}
	}
	rec := httptest.NewRecorder()
	rt.remote.Forward(rec, r)
	body := rec.Body.Bytes()
	if rec.Code/100 == 2 && strings.Contains(r.URL.Path, "/models/") {
		var v map[string]any
		if json.Unmarshal(body, &v) == nil && v["version"] != nil {
			v["engine"] = worker.EngineWorker
			body, _ = json.Marshal(v)
		}
	}
	if ct := rec.Header().Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(body)
}

// specDatasets are the datasets a training reads: its dataset, and a
// scenario's data roles'.
func specDatasets(spec worker.TrainSpec) []string {
	var out []string
	if spec.Dataset != "" {
		out = append(out, spec.Dataset)
	}
	for _, role := range spec.Data {
		if d := role["dataset"]; d != "" && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	slices.Sort(out)
	return out
}

func withBody(r *http.Request, raw []byte) *http.Request {
	r2 := r.Clone(r.Context())
	r2.Body = io.NopCloser(bytes.NewReader(raw))
	r2.ContentLength = int64(len(raw))
	return r2
}

// versions lists both engines' versions of the model, newest first, each
// with the engine that holds it.
func (rt *router) versions(r *http.Request) (any, error) {
	ctx, vhost, name := r.Context(), r.PathValue("vhost"), r.PathValue("name")
	mine, err := rt.e.models.versions(ctx, vhost, name)
	var ae *apiError
	if err != nil && (!errors.As(err, &ae) || ae.status != http.StatusNotFound) {
		return nil, err
	}
	theirs, rerr := rt.remote.Versions(ctx, vhost, name)
	if rerr != nil && !errors.Is(rerr, worker.ErrNotFound) {
		slog.Debug("ml: the hermod-ml worker did not list a model's versions", "model", name, "error", rerr)
	}
	if len(mine)+len(theirs) == 0 {
		return nil, err
	}
	for i := range theirs {
		theirs[i].Engine = worker.EngineWorker
	}
	all := slices.Concat(mine, theirs)
	slices.SortStableFunc(all, func(a, b worker.Version) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return map[string]any{"versions": all}, nil
}

// versionFile serves a version's file (its graph, its recommender, its
// export) from the engine that holds the version.
func (rt *router) versionFile(w http.ResponseWriter, r *http.Request) {
	if worker.IsBuiltinVersion(r.PathValue("version")) {
		rt.e.ServeHTTP(w, r)
		return
	}
	rt.remote.Forward(w, r)
}

// deleteModel removes the model's versions from both engines.
func (rt *router) deleteModel(r *http.Request) (any, error) {
	ctx, vhost, name := r.Context(), r.PathValue("vhost"), r.PathValue("name")
	err := rt.e.models.delete(ctx, vhost, name)
	rt.e.evict(vhost, name)
	rerr := rt.remote.DeleteModel(ctx, vhost, name)
	return nil, bothDeleted("model", err, rerr)
}
