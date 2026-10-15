// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"

	"github.com/gsoultan/hermod/pkg/ml/builtin/table"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// apiError is a refusal with the status the worker answers it with.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return &apiError{status: http.StatusBadRequest, msg: fmt.Sprintf(format, args...)}
}

func notFound(format string, args ...any) error {
	return &apiError{status: http.StatusNotFound, msg: fmt.Sprintf(format, args...)}
}

// needsWorker refuses what only the hermod-ml worker does.
func needsWorker(what string) error {
	return badRequest("%s %s: connect one on the Machine Learning page, or set HERMOD_ML_WORKER_URL.", what, NeedsWorker)
}

// notBuiltin refuses a version the engine did not train: the worker's
// (numbered 1, 2, …), which only the worker serves, so it is unavailable
// while no worker is connected rather than missing.
func notBuiltin(model, version string) error {
	if _, err := strconv.Atoi(version); err != nil {
		return notFound("Model %q has no version %q.", model, version)
	}
	return &apiError{status: http.StatusServiceUnavailable, msg: fmt.Sprintf("Version %s of model %q was trained by "+
		"the hermod-ml worker, so serving it %s: connect one on the Machine Learning page, or set HERMOD_ML_WORKER_URL.",
		version, model, NeedsWorker)}
}

func (e *Engine) routes() {
	m := http.NewServeMux()
	m.HandleFunc("GET /v2/health/live", func(w http.ResponseWriter, _ *http.Request) { reply(w, map[string]bool{"live": true}) })
	m.HandleFunc("GET /v2/health/ready", func(w http.ResponseWriter, _ *http.Request) { reply(w, map[string]bool{"ready": true}) })
	m.HandleFunc("GET /v1/capabilities", func(w http.ResponseWriter, _ *http.Request) { reply(w, capabilities()) })

	m.HandleFunc("POST /v1/datasets/{vhost}/{name}/rows", e.handle(e.appendRows))
	m.HandleFunc("PUT /v1/datasets/{vhost}/{name}/file", e.handle(e.uploadFile))
	m.HandleFunc("GET /v1/datasets/{vhost}", e.handle(func(r *http.Request) (any, error) {
		list, err := e.datasets.list(r.Context(), r.PathValue("vhost"))
		return map[string]any{"datasets": list}, err
	}))
	m.HandleFunc("GET /v1/datasets/{vhost}/{name}", e.handle(e.getDataset))
	m.HandleFunc("DELETE /v1/datasets/{vhost}/{name}", e.handle(func(r *http.Request) (any, error) {
		return nil, e.datasets.delete(r.Context(), r.PathValue("vhost"), r.PathValue("name"))
	}))
	m.HandleFunc("GET /v1/datasets/{vhost}/{name}/sample", e.handle(e.sampleDataset))
	m.HandleFunc("GET /v1/datasets/{vhost}/{name}/export", e.handle(refuse("Copying a dataset to a worker pool")))
	m.HandleFunc("PUT /v1/datasets/{vhost}/{name}/import", e.handle(refuse("Copying a dataset from a worker pool")))

	m.HandleFunc("POST /v1/models/{vhost}/{name}/train", e.handle(e.train))
	m.HandleFunc("POST /v1/train-check/{vhost}", e.handle(e.trainCheck))
	m.HandleFunc("POST /v1/recommend-check/{vhost}", e.handle(refuse("A recommendation scenario's data health check")))
	m.HandleFunc("POST /v1/models/{vhost}/{name}/train-custom", e.handle(refuse("A custom training script")))
	m.HandleFunc("GET /v1/models/{vhost}/{name}/versions", e.handle(func(r *http.Request) (any, error) {
		vs, err := e.models.versions(r.Context(), r.PathValue("vhost"), r.PathValue("name"))
		return map[string]any{"versions": vs}, err
	}))
	m.HandleFunc("GET /v1/models/{vhost}/{name}/versions/{version}/model.onnx", e.handle(func(r *http.Request) (any, error) {
		if !worker.IsBuiltinVersion(r.PathValue("version")) {
			return nil, notBuiltin(r.PathValue("name"), r.PathValue("version"))
		}
		return nil, notFound("Model %q was trained by Hermod's built-in engine, which keeps no ONNX graph: "+
			"it is always scored in Hermod's own process.", r.PathValue("name"))
	}))
	m.HandleFunc("GET /v1/models/{vhost}/{name}/versions/{version}/model.rec", e.recommender)
	m.HandleFunc("GET /v1/models/{vhost}/{name}/versions/{version}/export", e.handle(refuse("Exporting a version")))
	m.HandleFunc("POST /v1/models/{vhost}/{name}/import", e.handle(refuse("Importing a version")))
	m.HandleFunc("DELETE /v1/models/{vhost}/{name}", e.handle(func(r *http.Request) (any, error) {
		vhost, name := r.PathValue("vhost"), r.PathValue("name")
		err := e.models.delete(r.Context(), vhost, name)
		e.evict(vhost, name)
		return nil, err
	}))
	m.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fail(w, notFound("Not found."))
	})
	e.mux = m
}

func refuse(what string) func(*http.Request) (any, error) {
	return func(*http.Request) (any, error) { return nil, needsWorker(what) }
}

// handle renders fn's answer as JSON, nothing (204) for a nil answer, or its
// error as {"error": message}.
func (e *Engine) handle(fn func(*http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := fn(r)
		if err != nil {
			fail(w, err)
			return
		}
		if out == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		reply(w, out)
	}
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	status, msg := http.StatusInternalServerError, "Internal error: "+err.Error()
	var ae *apiError
	switch {
	case errors.As(err, &ae):
		status, msg = ae.status, ae.msg
	case errors.Is(err, errTooLarge), errors.Is(err, ErrBlobTooLarge):
		status, msg = http.StatusRequestEntityTooLarge, err.Error()
	default:
		slog.Error("ml: the built-in engine failed", "error", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

var errTooLarge = errors.New("the request body is larger than the limit")

// body reads a request body of at most the engine's upload limit.
func (e *Engine) body(r *http.Request) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, e.cfg.MaxUploadBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > e.cfg.MaxUploadBytes {
		return nil, fmt.Errorf("%w of %d MB", errTooLarge, e.cfg.MaxUploadBytes>>20)
	}
	return raw, nil
}

func (e *Engine) appendRows(r *http.Request) (any, error) {
	vhost, name := r.PathValue("vhost"), r.PathValue("name")
	if err := checkDatasetName(vhost, name); err != nil {
		return nil, err
	}
	raw, err := e.body(r)
	if err != nil {
		return nil, err
	}
	var body struct {
		Rows    json.RawMessage `json:"rows"`
		Replace *bool           `json:"replace"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, badRequest("The request body is not valid JSON: %v", err)
	}
	if body.Rows == nil {
		return nil, badRequest("'rows' is required.")
	}
	t, err := table.FromJSON(body.Rows)
	if err != nil {
		return nil, badRequest("%s.", err)
	}
	total, err := e.datasets.append(r.Context(), vhost, name, t, body.Replace != nil && *body.Replace)
	if err != nil {
		return nil, err
	}
	return map[string]any{"name": name, "rows": total}, nil
}

func (e *Engine) uploadFile(r *http.Request) (any, error) {
	vhost, name := r.PathValue("vhost"), r.PathValue("name")
	if err := checkDatasetName(vhost, name); err != nil {
		return nil, err
	}
	format := r.URL.Query().Get("format")
	if !slices.Contains(worker.FileFormats, format) {
		return nil, badRequest("Query parameter 'format' must be 'csv', 'xlsx' or 'parquet'.")
	}
	raw, err := e.body(r)
	if err != nil {
		return nil, err
	}
	var t *table.Table
	switch format {
	case "csv":
		if len(bytes.TrimSpace(raw)) == 0 {
			return nil, badRequest("The CSV file is empty.")
		}
		t, err = table.ParseCSV(bytes.NewReader(raw))
	case "xlsx":
		t, err = table.ParseXLSX(raw)
	default:
		t, err = table.ParseParquet(raw)
	}
	if err != nil {
		return nil, badRequest("%s.", err)
	}
	if err := e.datasets.replace(r.Context(), vhost, name, t); err != nil {
		return nil, err
	}
	return e.datasets.info(r.Context(), vhost, name)
}

// sampleRows is how many rows a dataset's description shows.
const sampleRows = 20

func (e *Engine) getDataset(r *http.Request) (any, error) {
	vhost, name := r.PathValue("vhost"), r.PathValue("name")
	info, err := e.datasets.info(r.Context(), vhost, name)
	if err != nil {
		return nil, err
	}
	t, err := e.datasets.load(r.Context(), vhost, name, sampleRows)
	if err != nil {
		return nil, err
	}
	info.Sample = t.Records(0, sampleRows)
	return info, nil
}

func (e *Engine) recommender(w http.ResponseWriter, r *http.Request) {
	v, raw, err := e.models.file(r.Context(), r.PathValue("vhost"), r.PathValue("name"), r.PathValue("version"), recommenderFile)
	if err == nil && v.Task != worker.TaskRecommendation {
		err = notFound("Version %s of model %q is not a recommendation model.", v.Version, r.PathValue("name"))
	}
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(raw) //nolint:gosec // G705: an artifact the engine wrote, sent as octet-stream to Hermod's own client, never rendered.
}
