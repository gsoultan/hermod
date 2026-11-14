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
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

type scopedLogStorage struct {
	storage.Storage
	logs []storage.Log
}

func (s *scopedLogStorage) ListLogs(_ context.Context, f storage.LogFilter) ([]storage.Log, int, error) {
	var out []storage.Log
	for _, l := range s.logs {
		if (f.WorkflowID == "" || l.WorkflowID == f.WorkflowID) &&
			(f.SourceID == "" || l.SourceID == f.SourceID) &&
			(f.SinkID == "" || l.SinkID == f.SinkID) {
			out = append(out, l)
		}
	}
	return out, len(out), nil
}

func (s *scopedLogStorage) GetWorkflow(_ context.Context, id string) (storage.Workflow, error) {
	switch id {
	case "wf-sales":
		return storage.Workflow{ID: id, VHost: "sales"}, nil
	case "wf-ops":
		return storage.Workflow{ID: id, VHost: "ops"}, nil
	}
	return storage.Workflow{}, storage.ErrNotFound
}

func (s *scopedLogStorage) GetSource(_ context.Context, id string) (storage.Source, error) {
	if id == "src-sales" {
		return storage.Source{ID: id, VHost: "sales"}, nil
	}
	return storage.Source{}, storage.ErrNotFound
}

func (s *scopedLogStorage) GetSink(_ context.Context, id string) (storage.Sink, error) {
	if id == "snk-ops" {
		return storage.Sink{ID: id, VHost: "ops"}, nil
	}
	return storage.Sink{}, storage.ErrNotFound
}

func listLogsAs(t *testing.T, user *storage.User, target string) (int, []string) {
	t.Helper()
	store := &scopedLogStorage{logs: []storage.Log{
		{ID: "l-sales-wf", WorkflowID: "wf-sales", Message: "Created workflow Sales orders"},
		{ID: "l-sales-src", SourceID: "src-sales", Message: "source connected to db.sales.internal"},
		{ID: "l-ops-wf", WorkflowID: "wf-ops", Message: "Created workflow Ops alerts"},
		{ID: "l-ops-snk", SinkID: "snk-ops", Message: "sink write failed"},
		{ID: "l-gone", WorkflowID: "wf-deleted", Message: "Deleted workflow"},
		{ID: "l-platform", Message: "Deleted vhost finance"},
	}}
	mux := http.NewServeMux()
	NewLogHandler(&handlers.Handler{Storage: store, LogStorage: store}).RegisterLogRoutes(mux)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, user))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var body struct {
		Data []storage.Log `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	ids := []string{}
	for _, l := range body.Data {
		ids = append(ids, l.ID)
	}
	return rec.Code, ids
}

// The log names workflows, sources and sinks and says what happened to them. It
// was listed whole to every session, so a user given one vhost read the
// activity of all of them.
func TestLogsAreHeldToTheCallersVHosts(t *testing.T) {
	ops := &storage.User{ID: "u-ops", Role: storage.RoleViewer, VHosts: []string{"ops"}}

	code, ids := listLogsAs(t, ops, "/api/logs")
	if want := "[l-ops-wf l-ops-snk]"; code != http.StatusOK || fmt.Sprint(ids) != want {
		t.Errorf("a Viewer of ops listing the log got %d %v, want 200 %s: the entries of "+
			"another vhost, of something deleted and of the platform are not theirs", code, ids, want)
	}

	for _, target := range []string{"/api/logs?workflow_id=wf-sales", "/api/logs?source_id=src-sales"} {
		if code, ids := listLogsAs(t, ops, target); code != http.StatusForbidden || len(ids) != 0 {
			t.Errorf("a Viewer of ops asking %s got %d %v, want 403", target, code, ids)
		}
	}

	if code, ids := listLogsAs(t, ops, "/api/logs?workflow_id=wf-ops"); code != http.StatusOK || fmt.Sprint(ids) != "[l-ops-wf]" {
		t.Errorf("a Viewer of ops asking for its own workflow got %d %v, want 200 [l-ops-wf]", code, ids)
	}

	admin := &storage.User{ID: "u-admin", Role: storage.RoleAdministrator}
	if code, ids := listLogsAs(t, admin, "/api/logs"); code != http.StatusOK || len(ids) != 6 {
		t.Errorf("an Administrator listing the log got %d %v, want all six entries", code, ids)
	}
}
