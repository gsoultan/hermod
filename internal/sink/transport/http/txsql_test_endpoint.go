// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/sink/txsql"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

// txsqlTestTimeout bounds a Test run. Every statement also has its own
// timeout, and lock waits are capped inside the sandbox, so this is the
// backstop for a long statement list.
const txsqlTestTimeout = 60 * time.Second

const maxTxsqlRequestBytes = 1 << 20

type txsqlTestRequest struct {
	VHost string `json:"vhost"`
	// StringMap: the form keeps a boolean or two in its config, and a plain
	// string map refuses the whole request.
	Config hermod.StringMap `json:"config"`
	Sample map[string]any   `json:"sample"`
}

type txsqlTestResponse struct {
	Simulation hermod.SinkSimulation `json:"simulation"`
	Sample     map[string]any        `json:"sample"`
}

// TestTxSQLSink runs a Transactional SQL sink's statements once against its
// database with a sample message, inside a transaction that is always rolled
// back, and reports what each statement bound, returned and changed.
func (h *SinkHandler) TestTxSQLSink(w http.ResponseWriter, r *http.Request) {
	var req txsqlTestRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTxsqlRequestBytes)).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !h.AllowConnectorVHost(w, r, req.VHost) {
		return
	}
	// Refused before anything connects, in the words saving it would use.
	if _, err := txsql.ParseConfig(req.Config); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.Registry == nil {
		h.JsonError(w, "the engine is not running here, so the statements cannot be tested", http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), txsqlTestTimeout)
	defer cancel()

	snk, err := h.Registry.CreateSink(ctx, factory.SinkConfig{Type: "txsql", VHost: req.VHost, Config: req.Config})
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer func() { _ = snk.Close() }()
	sim, ok := hermod.SimulatorOf(snk)
	if !ok {
		h.JsonError(w, "this sink cannot be tested without writing", http.StatusBadRequest)
		return
	}

	sample, msg := fcmSampleMessage(req.Sample)
	defer message.ReleaseMessage(msg)

	// The same sandbox a workflow preview runs in: one transaction, lock
	// waits capped, rolled back whatever happens.
	sb := sqlutil.NewSandbox(ctx)
	rep, err := sim.Simulate(sqlutil.WithSandbox(ctx, sb), msg)
	if rbErr := sb.Rollback(); rbErr != nil {
		rep.RolledBack = false
		rep.Error = errors.Join(errorOrNil(rep.Error), rbErr).Error()
	}
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(txsqlTestResponse{Simulation: rep, Sample: sample})
}

func errorOrNil(s string) error {
	if s == "" {
		return nil
	}
	return errors.New(s)
}

// CompileTxSQLSink returns the statements a sink's config runs: written out
// in SQL mode, compiled from its steps in Guided mode. Show SQL and Convert to
// SQL mode use it, so the form never has a compiler of its own to drift.
func (h *SinkHandler) CompileTxSQLSink(w http.ResponseWriter, r *http.Request) {
	var req txsqlTestRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTxsqlRequestBytes)).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg, err := txsql.ParseConfig(req.Config)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"statements": cfg.Statements})
}
