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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/pkg/infra/state"
)

// ---------------------------------------------------------------------------
// Which sinks the editor may offer as transactional group members.
//
// The workflow editor has to filter its member picker to sinks that can take
// part in two-phase commit, and it cannot work that out for itself. The
// alternative — a list of connector capabilities kept by hand in TypeScript — is
// the shape of claim that has gone stale repeatedly in this repository, and the
// cost of it being wrong here is a group the user builds in the editor that
// refuses to start when the workflow runs.
//
// So the backend answers, from the same list the engine enforces.
// ---------------------------------------------------------------------------

func TestTwoPhaseCapabilityMatchesWhatTheEngineEnforces(t *testing.T) {
	h := &SinkHandler{}
	rec := httptest.NewRecorder()
	h.ListTwoPhaseCapableSinkTypes(rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/sinks/capabilities/two-phase", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}

	var got struct {
		Types []string `json:"types"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	want := factory.TwoPhaseCapableSinkTypes()
	if len(got.Types) != len(want) {
		t.Fatalf("served %v, engine enforces %v", got.Types, want)
	}
	for _, sinkType := range want {
		if !slices.Contains(got.Types, sinkType) {
			t.Errorf("%q can join a group but is not served to the editor, so nobody can select it", sinkType)
		}
	}
}

// TestTheCapabilityListIsNotEmpty: an empty list disables the member picker
// entirely, which looks like the feature being unavailable rather than broken.
func TestTheCapabilityListIsNotEmpty(t *testing.T) {
	h := &SinkHandler{}
	rec := httptest.NewRecorder()
	h.ListTwoPhaseCapableSinkTypes(rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/sinks/capabilities/two-phase", nil))

	var got struct {
		Types []string `json:"types"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if len(got.Types) == 0 {
		t.Error("no sink type is offered, so a transactional group can never be assembled in the editor")
	}
}

// The editor also needs to know whether a group could start at all. Without a
// durable state store every group is refused when its workflow runs, which out
// of the box is every group — so the form says so, and says where to fix it,
// using the same rule the engine enforces.
func TestTheCapabilityReportsWhetherTheTransactionLogIsReady(t *testing.T) {
	decode := func(t *testing.T, h *SinkHandler) (ready bool, problem string) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ListTwoPhaseCapableSinkTypes(rec,
			httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/sinks/capabilities/two-phase", nil))
		var got struct {
			LogReady   *bool  `json:"log_ready"`
			LogProblem string `json:"log_problem"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if got.LogReady == nil {
			t.Fatal("the response does not say whether the transaction log is ready")
		}
		return *got.LogReady, got.LogProblem
	}

	reg := registry.NewRegistry(nil)
	h := &SinkHandler{Handler: &handlers.Handler{Registry: reg}}

	ready, problem := decode(t, h)
	if ready || problem != reg.TxGroupLogProblem() || problem == "" {
		t.Errorf("no state store: ready=%v problem=%q, want not ready with the engine's reason", ready, problem)
	}

	ss, err := state.NewStateStore(state.Config{Type: "sqlite", Path: t.TempDir() + "/state.db"})
	if err != nil {
		t.Fatal(err)
	}
	reg.SetStateStore(ss)
	if ready, problem := decode(t, h); !ready || problem != "" {
		t.Errorf("SQLite state store: ready=%v problem=%q, want ready", ready, problem)
	}
}
