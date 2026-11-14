// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// An audit row has to say which vhost it is about, or a vhost's audit
// retention has nothing to act on.

type auditVHostStore struct {
	storage.Storage
	workflows map[string]storage.Workflow
	sources   map[string]storage.Source
	sinks     map[string]storage.Sink
	audits    []storage.AuditLog
}

func (s *auditVHostStore) CreateLog(context.Context, storage.Log) error { return nil }

func (s *auditVHostStore) CreateAuditLog(_ context.Context, l storage.AuditLog) error {
	s.audits = append(s.audits, l)
	return nil
}

func (s *auditVHostStore) GetWorkflow(_ context.Context, id string) (storage.Workflow, error) {
	if wf, ok := s.workflows[id]; ok {
		return wf, nil
	}
	return storage.Workflow{}, storage.ErrNotFound
}

func (s *auditVHostStore) GetSource(_ context.Context, id string) (storage.Source, error) {
	if src, ok := s.sources[id]; ok {
		return src, nil
	}
	return storage.Source{}, storage.ErrNotFound
}

func (s *auditVHostStore) GetSink(_ context.Context, id string) (storage.Sink, error) {
	if snk, ok := s.sinks[id]; ok {
		return snk, nil
	}
	return storage.Sink{}, storage.ErrNotFound
}

func TestAnAuditRowNamesTheVHostItIsAbout(t *testing.T) {
	store := &auditVHostStore{
		workflows: map[string]storage.Workflow{"wf-1": {ID: "wf-1", VHost: "sales"}, "wf-shared": {ID: "wf-shared"}},
		sources:   map[string]storage.Source{"src-1": {ID: "src-1", VHost: "ops"}},
		sinks:     map[string]storage.Sink{"snk-1": {ID: "snk-1", VHost: "ops"}},
	}
	h := &Handler{Storage: store, LogStorage: store}

	for _, tc := range []struct {
		name                       string
		workflowID, source, sinkID string
		data                       any
		want                       string
	}{
		{name: "a workflow action with no payload", workflowID: "wf-1", want: "sales"},
		{name: "a workflow in no vhost lives in default", workflowID: "wf-shared", want: "default"},
		{name: "a source", source: "src-1", want: "ops"},
		{name: "a sink", sinkID: "snk-1", want: "ops"},
		{name: "a saved workflow says so itself", workflowID: "wf-new", data: storage.Workflow{ID: "wf-new", VHost: "finance"}, want: "finance"},
		{name: "a deleted workflow still says so in what was deleted", workflowID: "wf-gone", data: map[string]any{"vhost": "finance"}, want: "finance"},
		{name: "a vhost's own setting", workflowID: "sales", source: "vhost", data: map[string]string{"secret_name": "API_KEY"}, want: "sales"},
		{name: "a created vhost", workflowID: "id-123", source: "vhost", data: storage.VHost{ID: "id-123", Name: "legal"}, want: "legal"},
		{name: "a user belongs to no vhost", workflowID: "u-1", source: "user", want: ""},
		{name: "a deleted workflow nobody can find", workflowID: "wf-missing", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store.audits = nil
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/x", nil)
			h.RecordAuditLog(r, "INFO", "did it", "update", tc.workflowID, tc.source, tc.sinkID, tc.data)
			if len(store.audits) != 1 {
				t.Fatalf("wrote %d audit rows, want 1", len(store.audits))
			}
			if got := store.audits[0].VHost; got != tc.want {
				raw, _ := json.Marshal(store.audits[0])
				t.Errorf("audit row is in vhost %q, want %q: %s", got, tc.want, raw)
			}
		})
	}
}
