// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
)

// renameStore is a control plane whose vhost can be renamed the way
// UpdateVHost does it: the workflow, its source and sink, and the vhost's
// secrets all move to the new name together.
type renameStore struct {
	mockReconStorage
	resMu   sync.Mutex
	sources map[string]storage.Source
	sinks   map[string]storage.Sink
	secrets map[string]string // "vhost/name" -> value
}

func (s *renameStore) GetSource(_ context.Context, id string) (storage.Source, error) {
	s.resMu.Lock()
	defer s.resMu.Unlock()
	src, ok := s.sources[id]
	if !ok {
		return storage.Source{}, storage.ErrNotFound
	}
	return src, nil
}

func (s *renameStore) GetSink(_ context.Context, id string) (storage.Sink, error) {
	s.resMu.Lock()
	defer s.resMu.Unlock()
	snk, ok := s.sinks[id]
	if !ok {
		return storage.Sink{}, storage.ErrNotFound
	}
	return snk, nil
}

func (s *renameStore) ListSources(context.Context, storage.CommonFilter) ([]storage.Source, int, error) {
	s.resMu.Lock()
	defer s.resMu.Unlock()
	var out []storage.Source
	for _, src := range s.sources {
		out = append(out, src)
	}
	return out, len(out), nil
}

func (s *renameStore) ListSinks(context.Context, storage.CommonFilter) ([]storage.Sink, int, error) {
	s.resMu.Lock()
	defer s.resMu.Unlock()
	var out []storage.Sink
	for _, snk := range s.sinks {
		out = append(out, snk)
	}
	return out, len(out), nil
}

func (s *renameStore) ListVHostSecrets(context.Context, string) ([]storage.VHostSecret, error) {
	return nil, nil
}

func (s *renameStore) GetVHostSecret(_ context.Context, vhost, name string) (storage.VHostSecret, error) {
	s.resMu.Lock()
	defer s.resMu.Unlock()
	v, ok := s.secrets[vhost+"/"+name]
	if !ok {
		return storage.VHostSecret{}, storage.ErrNotFound
	}
	return storage.VHostSecret{VHost: vhost, Name: name, Value: v}, nil
}

func (s *renameStore) PutVHostSecret(context.Context, storage.VHostSecret) error { return nil }
func (s *renameStore) DeleteVHostSecret(context.Context, string, string) error   { return nil }
func (s *renameStore) DeleteVHostSecrets(context.Context, string) error          { return nil }

var _ storage.VHostSecretStore = (*renameStore)(nil)

func (s *renameStore) rename(from, to string) {
	s.mu.Lock()
	for id, wf := range s.workflows {
		if wf.VHost == from {
			wf.VHost = to
			s.workflows[id] = wf
		}
	}
	s.mu.Unlock()

	s.resMu.Lock()
	defer s.resMu.Unlock()
	for id, src := range s.sources {
		if src.VHost == from {
			src.VHost = to
			s.sources[id] = src
		}
	}
	for id, snk := range s.sinks {
		if snk.VHost == from {
			snk.VHost = to
			s.sinks[id] = snk
		}
	}
	moved := map[string]string{}
	for key, v := range s.secrets {
		if len(key) > len(from) && key[:len(from)+1] == from+"/" {
			moved[to+key[len(from):]] = v
			delete(s.secrets, key)
		}
	}
	for k, v := range moved {
		s.secrets[k] = v
	}
}

// Renaming a vhost restarts each of its running workflows under the new name.
// The restart has to build the workflow's source and sink from what storage
// holds now: the registry used to build them from the copies it cached when
// the workflow first started, still under the old name, so a connector that
// reads a vhost secret looked for it under a name that no longer held it, the
// restart failed, and every workflow of the vhost stayed stopped.
func TestRenamingAVHostKeepsItsWorkflowsRunning(t *testing.T) {
	store := &renameStore{
		mockReconStorage: mockReconStorage{workflows: map[string]storage.Workflow{}},
		sources: map[string]storage.Source{
			"s1": {ID: "s1", Type: "test-source", VHost: "sales", Config: map[string]string{"password": "{{secret:DB_PASSWORD}}"}},
		},
		sinks: map[string]storage.Sink{
			"k1": {ID: "k1", Type: "test-sink", VHost: "sales", Config: map[string]string{"token": "{{secret:DB_PASSWORD}}"}},
		},
		secrets: map[string]string{"sales/DB_PASSWORD": "pw"},
	}
	store.workflows["wf1"] = storage.Workflow{
		ID: "wf1", VHost: "sales", Active: true,
		Nodes: []storage.WorkflowNode{{ID: "n1", Type: "source", RefID: "s1"}, {ID: "n2", Type: "sink", RefID: "k1"}},
		Edges: []storage.WorkflowEdge{{ID: "e1", SourceID: "n1", TargetID: "n2"}},
	}

	reg := registry.NewRegistry(store)
	t.Cleanup(reg.StopAll)
	var mu sync.Mutex
	var sourceVHosts, sinkVHosts []string
	reg.SetFactories(
		func(cfg factory.SourceConfig) (hermod.Source, error) {
			mu.Lock()
			sourceVHosts = append(sourceVHosts, cfg.VHost)
			mu.Unlock()
			return &mockSource{}, nil
		},
		func(cfg factory.SinkConfig) (hermod.Sink, error) {
			mu.Lock()
			sinkVHosts = append(sinkVHosts, cfg.VHost)
			mu.Unlock()
			return &mockSink{}, nil
		},
	)
	w := NewWorker(store, reg)
	w.SetWorkerConfig(0, 1, "", "")

	w.sync(t.Context(), true)
	if !reg.IsEngineRunning("wf1") {
		t.Fatal("the workflow did not start before the rename")
	}

	store.rename("sales", "revenue")
	w.sync(t.Context(), false)

	deadline := time.Now().Add(2 * time.Second)
	for !reg.IsEngineRunning("wf1") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !reg.IsEngineRunning("wf1") {
		wf, _ := store.GetWorkflow(t.Context(), "wf1")
		t.Fatalf("the workflow stopped after its vhost was renamed; status %q", wf.Status)
	}
	if cfg, _ := reg.GetWorkflowConfig("wf1"); cfg.VHost != "revenue" {
		t.Errorf("the running workflow's vhost = %q, want revenue", cfg.VHost)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := sourceVHosts[len(sourceVHosts)-1]; got != "revenue" {
		t.Errorf("the restarted source was built for vhost %q, want revenue", got)
	}
	if got := sinkVHosts[len(sinkVHosts)-1]; got != "revenue" {
		t.Errorf("the restarted sink was built for vhost %q, want revenue", got)
	}
}
