// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"time"
)

// Local is an ML engine in Hermod's own process, such as the built-in engine
// (pkg/ml/builtin). It answers the worker's routes as an http.Handler, so a
// Client built on it with NewLocal works as one for a worker does, and scores
// rows with Predict directly, without the Open Inference Protocol.
type Local interface {
	http.Handler
	// Predict scores rows with a version of a model, one prediction per row:
	// {"label", "probability"} for a classifier, {"value"} for a regression,
	// as the worker's serving answers.
	Predict(ctx context.Context, vhost, model, version string, rows []map[string]any) ([]map[string]any, error)
}

// LocalURL is the base URL of a Client built with NewLocal. Its calls never
// leave the process, so it names no host on any network.
const LocalURL = "http://hermod-ml.local"

// NewLocal returns a client whose calls l answers in-process, with
// HERMOD_ML_TRAIN_TIMEOUT applied when set.
func NewLocal(l Local) *Client {
	c := New(LocalURL, "", &http.Client{Transport: localTransport{l}})
	if d, err := time.ParseDuration(os.Getenv("HERMOD_ML_TRAIN_TIMEOUT")); err == nil && d > 0 {
		c.TrainTimeout = d
	}
	c.local = l
	return c
}

// Local returns the in-process engine the client calls, or nil for a worker
// reached over HTTP.
func (c *Client) Local() Local { return c.local }

// localTransport hands a request to the engine's handler and returns what it
// wrote. The request's context goes with it, so a caller that gives up, or
// the client's timeout, stops the engine's work.
type localTransport struct{ h http.Handler }

func (t localTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, req)
	if req.Body != nil {
		_ = req.Body.Close()
	}
	resp := rec.Result()
	resp.Request = req
	return resp, req.Context().Err()
}
