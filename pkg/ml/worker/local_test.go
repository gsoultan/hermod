// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
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
	"errors"
	"net/http"
	"testing"
)

// fakeLocal answers the worker's routes in-process, as the built-in engine
// does, and counts its predictions.
type fakeLocal struct {
	paths []string
	auth  []string
}

func (f *fakeLocal) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.paths = append(f.paths, r.Method+" "+r.URL.Path)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	switch r.URL.Path {
	case "/v2/health/ready":
		w.WriteHeader(http.StatusOK)
	case "/v1/capabilities":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tasks":["auto"],"algorithms":["auto","mlp"],"unavailable":{"xgboost":"needs the hermod-ml worker"}}`))
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Model \"m\" has no versions in vhost \"v\"."}`))
	}
}

func (f *fakeLocal) Predict(context.Context, string, string, string, []map[string]any) ([]map[string]any, error) {
	return []map[string]any{{"value": 1.0}}, nil
}

func TestNewLocalReachesTheEngineInProcess(t *testing.T) {
	f := &fakeLocal{}
	c := NewLocal(f)
	if c.Local() != f {
		t.Fatal("Local() is not the engine the client was built on")
	}
	if err := c.Ready(context.Background()); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	caps, err := c.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if len(caps.Algorithms) != 2 || caps.Unavailable["xgboost"] == "" {
		t.Errorf("capabilities = %+v", caps)
	}
	// The engine's errors map as a worker's do.
	if _, err := c.Versions(context.Background(), "v", "m"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Versions error = %v, want ErrNotFound", err)
	}
	if len(f.paths) != 3 || f.paths[0] != "GET /v2/health/ready" {
		t.Errorf("engine saw %v", f.paths)
	}
	for _, a := range f.auth {
		if a != "" {
			t.Errorf("an in-process call carried a token: %q", a)
		}
	}
}

func TestARemoteWorkerIsNotLocal(t *testing.T) {
	if New("http://worker:8080", "", nil).Local() != nil {
		t.Error("a worker over HTTP reports an in-process engine")
	}
}

func TestNewLocalHonoursTheTrainTimeout(t *testing.T) {
	t.Setenv("HERMOD_ML_TRAIN_TIMEOUT", "90s")
	if got := NewLocal(&fakeLocal{}).TrainTimeout.String(); got != "1m30s" {
		t.Errorf("TrainTimeout = %s, want 1m30s", got)
	}
}

func TestProviderFallsBackToTheBuiltinEngine(t *testing.T) {
	builtin := NewLocal(&fakeLocal{})
	p := NewProvider(func() *Client { return nil })
	if c, src := p.Current(); c != nil || src != SourceNone {
		t.Fatalf("Current() = %v, %s before an engine is set", c, src)
	}
	p.SetBuiltin(builtin)
	if c, src := p.Current(); c != builtin || src != SourceBuiltin {
		t.Errorf("Current() = %v, %s, want the built-in engine", c, src)
	}
	saved := New("http://saved:8080", "", nil)
	p.SetSaved(saved)
	if c, src := p.Current(); c != saved || src != SourceSaved {
		t.Errorf("a saved worker does not win over the built-in engine: %v, %s", c, src)
	}
	p.SetSaved(nil)
	if c, _ := p.Current(); c != builtin {
		t.Error("removing the saved worker does not fall back to the built-in engine")
	}
	env := New("http://env:8080", "", nil)
	pe := NewProvider(func() *Client { return env })
	pe.SetBuiltin(builtin)
	if c, src := pe.Current(); c != env || src != SourceEnvironment {
		t.Errorf("the environment's worker does not win: %v, %s", c, src)
	}
	if pe.Builtin() != builtin {
		t.Error("Builtin() is not the engine set")
	}
}
