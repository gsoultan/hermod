// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package sql

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// seedVHostContents gives vhost one of everything Hermod keeps by vhost name.
func seedVHostContents(t *testing.T, s *sqlStorage, vhost string) {
	t.Helper()
	ctx := t.Context()
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seeding %s of %s: %v", what, vhost, err)
		}
	}
	must("workflow", s.CreateWorkflow(ctx, storage.Workflow{ID: "wf-" + vhost, Name: "orders-" + vhost, VHost: vhost}))
	must("source", s.CreateSource(ctx, storage.Source{ID: "src-" + vhost, Name: "pg-" + vhost, Type: "postgres", VHost: vhost}))
	must("sink", s.CreateSink(ctx, storage.Sink{ID: "snk-" + vhost, Name: "kafka-" + vhost, Type: "kafka", VHost: vhost}))
	must("secret", s.PutVHostSecret(ctx, storage.VHostSecret{VHost: vhost, Name: "API_KEY", Value: "s3cret", UpdatedBy: "ada"}))
	must("model", s.PutMLModel(ctx, churnModel(vhost)))
	must("prediction log", s.InsertMLPredictionLogs(ctx, []storage.MLPredictionLog{{VHost: vhost, Model: "churn", Timestamp: at, Inputs: map[string]any{"age": 1}, Outputs: map[string]any{"p": 0.5}}}))
	_, err := s.PutMLScript(ctx, storage.MLScript{VHost: vhost, Name: "trees", Source: scriptV1, CreatedBy: "ada"})
	must("script", err)
	must("blob", s.PutMLBlob(ctx, vhost, "datasets/orders.csv", []byte("a,b\n1,2\n")))
	job := newJob("job-"+vhost, vhost, "churn", storage.MLJobPredict, at)
	must("job", s.CreateMLJob(ctx, job))
	must("job result", s.PutMLJobResult(ctx, vhost, job.ID, json.RawMessage(`{"ok":true}`)))
	limit := int64(7)
	must("quotas", s.PutMLQuotas(ctx, storage.MLQuotas{VHost: vhost, MaxDatasets: &limit}))
	must("schedule", s.PutMLSchedule(ctx, storage.MLSchedule{VHost: vhost, Kind: storage.MLScheduleDatasetRefresh, Name: "orders", Schedule: "@daily", Spec: []byte(`{}`)}))
	must("feedback", s.AddMLFeedback(ctx, []storage.MLFeedbackEvent{{VHost: vhost, Model: "churn", RecommendationID: "rec-1", ItemID: "i1", Outcome: "accepted", At: at}}))
	must("budget", s.PutAIBudget(ctx, sampleBudget(vhost)))
	_, err = s.AddAIUsage(ctx, vhost, "2026-11", "wf-"+vhost, storage.AIUsageDelta{InputTokens: 10})
	must("usage", err)
	must("dead letter", s.PutDeadLetter(ctx, sampleDeadLetter("dl-"+vhost, vhost, "wf-"+vhost, at)))
	must("audit log", s.CreateAuditLog(ctx, storage.AuditLog{Timestamp: at, Action: "UPDATE", EntityType: "workflow", EntityID: "wf-" + vhost, VHost: vhost}))
	must("retention", storage.SaveVHostRetention(ctx, s, vhost, storage.VHostRetention{AuditLogDays: 90}))
	must("dashboard sample", s.RecordDashboardSample(ctx, storage.DashboardSample{VHost: vhost, Timestamp: at, TotalProcessed: 3}))
}

// countVHostContents reports what of seedVHostContents is readable under
// vhost, by the same calls the rest of Hermod reads it with.
func countVHostContents(t *testing.T, s *sqlStorage, vhost string) map[string]bool {
	t.Helper()
	ctx := t.Context()
	found := map[string]bool{}
	if wfs, _, err := s.ListWorkflows(ctx, storage.CommonFilter{VHost: vhost}); err == nil {
		found["workflow"] = len(wfs) > 0
	}
	if srcs, _, err := s.ListSources(ctx, storage.CommonFilter{VHost: vhost}); err == nil {
		found["source"] = len(srcs) > 0
	}
	if snks, _, err := s.ListSinks(ctx, storage.CommonFilter{VHost: vhost}); err == nil {
		found["sink"] = len(snks) > 0
	}
	if sec, err := s.GetVHostSecret(ctx, vhost, "API_KEY"); err == nil {
		found["secret"] = sec.Value == "s3cret"
	}
	_, err := s.GetMLModel(ctx, vhost, "churn")
	found["model"] = err == nil
	if logs, err := s.ListMLPredictionLogs(ctx, vhost, "churn", 10); err == nil {
		found["prediction log"] = len(logs) > 0
	}
	_, err = s.GetMLScript(ctx, vhost, "trees")
	found["script"] = err == nil
	if data, err := s.GetMLBlob(ctx, vhost, "datasets/orders.csv"); err == nil {
		found["blob"] = string(data) == "a,b\n1,2\n"
	}
	_, err = s.GetMLJob(ctx, vhost, "job-"+vhost)
	if err != nil {
		// A job keeps the id it was given; find it under whichever vhost seeded it.
		jobs, _, lerr := s.ListMLJobs(ctx, storage.MLJobFilter{VHost: vhost})
		err = lerr
		if lerr == nil && len(jobs) == 0 {
			err = storage.ErrNotFound
		}
	}
	found["job"] = err == nil
	if q, err := s.GetMLQuotas(ctx, vhost); err == nil {
		found["quotas"] = q.MaxDatasets != nil && *q.MaxDatasets == 7
	}
	_, err = s.GetMLSchedule(ctx, vhost, storage.MLScheduleDatasetRefresh, "orders")
	found["schedule"] = err == nil
	if n, err := s.CountMLFeedback(ctx, vhost, "churn", time.Time{}); err == nil {
		found["feedback"] = n["accepted"] == 1
	}
	_, err = s.GetAIBudget(ctx, vhost)
	found["budget"] = err == nil
	if usage, err := s.ListAIUsage(ctx, vhost, "2026-11"); err == nil {
		found["usage"] = len(usage) > 0
	}
	if dls, _, err := s.ListDeadLetters(ctx, storage.DeadLetterFilter{VHosts: []string{vhost}}); err == nil {
		found["dead letter"] = slices.ContainsFunc(dls, func(d storage.DeadLetter) bool { return d.VHost == vhost })
	}
	if rows, _, err := s.ListAuditLogs(ctx, storage.AuditFilter{CommonFilter: storage.CommonFilter{VHost: vhost}}); err == nil {
		found["audit log"] = len(rows) > 0
	}
	if r, err := storage.LoadVHostRetention(ctx, s, vhost); err == nil {
		found["retention"] = r.AuditLogDays == 90
	}
	if hist, err := s.GetDashboardHistory(ctx, vhost, time.Time{}, 10); err == nil {
		found["dashboard sample"] = len(hist) > 0
	}
	return found
}

var vhostContents = []string{
	"workflow", "source", "sink", "secret", "model", "prediction log", "script", "blob", "job",
	"quotas", "schedule", "feedback", "budget", "usage", "dead letter", "dashboard sample",
	"audit log", "retention",
}

// Renaming a vhost must carry everything that belongs to it to the new name.
// Every one of these is stored by the vhost's name, so before the rename was
// carried over, each was left under a name no vhost had any more: the
// workflows and users the person was asking about, and everything else here.
func TestRenamingAVHostCarriesEverythingItHoldsToTheNewName(t *testing.T) {
	withKey(t, "vhost-rename-test-key")
	checkRenameCarriesEverything(t, newRotationStorage(t), "")
}

// The same against real PostgreSQL, where the rename runs as a transaction of
// rewritten placeholders and integer columns are read back as text.
func TestRenamingAVHostCarriesEverythingOnPostgres(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to enable")
	}
	withKey(t, "vhost-rename-test-key")
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening %s: %v", dsn, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewSQLStorage(db, "pgx").(*sqlStorage)
	if err := s.Init(t.Context()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Names and ids are unique per run, as the database outlives the test.
	checkRenameCarriesEverything(t, s, fmt.Sprintf("r%d-", time.Now().UnixNano()))
}

func checkRenameCarriesEverything(t *testing.T, s *sqlStorage, p string) {
	t.Helper()
	ctx := t.Context()
	sales, ops, revenue := p+"sales", p+"ops", p+"revenue"

	if err := s.CreateVHost(ctx, storage.VHost{ID: p + "vh-1", Name: sales}); err != nil {
		t.Fatalf("CreateVHost: %v", err)
	}
	if err := s.CreateVHost(ctx, storage.VHost{ID: p + "vh-2", Name: ops}); err != nil {
		t.Fatalf("CreateVHost: %v", err)
	}
	seedVHostContents(t, s, sales)
	seedVHostContents(t, s, ops)
	if err := s.CreateUser(ctx, storage.User{ID: p + "u-1", Username: p + "ada", Password: "x", Role: storage.RoleEditor, VHosts: []string{ops, sales}}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := s.UpdateVHost(ctx, storage.VHost{ID: p + "vh-1", Name: revenue, Description: "renamed"}); err != nil {
		t.Fatalf("UpdateVHost: %v", err)
	}

	vh, err := s.GetVHost(ctx, p+"vh-1")
	if err != nil || vh.Name != revenue || vh.Description != "renamed" {
		t.Fatalf("GetVHost = %+v, %v; want revenue/renamed", vh, err)
	}
	moved := countVHostContents(t, s, revenue)
	left := countVHostContents(t, s, sales)
	other := countVHostContents(t, s, ops)
	for _, what := range vhostContents {
		if !moved[what] {
			t.Errorf("the %s of the renamed vhost is not under its new name", what)
		}
		if left[what] {
			t.Errorf("a %s is still under the old name", what)
		}
		if !other[what] {
			t.Errorf("the %s of an unrelated vhost was disturbed", what)
		}
	}

	u, err := s.GetUser(ctx, p+"u-1")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if !slices.Equal(u.VHosts, []string{ops, revenue}) {
		t.Errorf("user vhosts = %v, want [%s %s]", u.VHosts, ops, revenue)
	}
}

// A rename onto a name another vhost already has fails, and fails whole: the
// first vhost keeps its name and nothing it holds has moved.
func TestRenamingAVHostOntoATakenNameChangesNothing(t *testing.T) {
	withKey(t, "vhost-rename-test-key")
	s := newRotationStorage(t)
	ctx := t.Context()

	if err := s.CreateVHost(ctx, storage.VHost{ID: "vh-1", Name: "sales"}); err != nil {
		t.Fatalf("CreateVHost: %v", err)
	}
	if err := s.CreateVHost(ctx, storage.VHost{ID: "vh-2", Name: "ops"}); err != nil {
		t.Fatalf("CreateVHost: %v", err)
	}
	seedVHostContents(t, s, "sales")

	err := s.UpdateVHost(ctx, storage.VHost{ID: "vh-1", Name: "ops"})
	if !errors.Is(err, storage.ErrVHostNameTaken) {
		t.Fatalf("UpdateVHost onto a taken name: err = %v, want ErrVHostNameTaken", err)
	}
	vh, _ := s.GetVHost(ctx, "vh-1")
	if vh.Name != "sales" {
		t.Errorf("the vhost was renamed to %q anyway", vh.Name)
	}
	stayed := countVHostContents(t, s, "sales")
	for _, what := range vhostContents {
		if !stayed[what] {
			t.Errorf("the %s moved although the rename failed", what)
		}
	}
}

// Saving a vhost under the name it already has only changes its description.
func TestUpdatingAVHostDescriptionLeavesItsContentsAlone(t *testing.T) {
	withKey(t, "vhost-rename-test-key")
	s := newRotationStorage(t)
	ctx := t.Context()
	if err := s.CreateVHost(ctx, storage.VHost{ID: "vh-1", Name: "sales"}); err != nil {
		t.Fatalf("CreateVHost: %v", err)
	}
	seedVHostContents(t, s, "sales")
	if err := s.UpdateVHost(ctx, storage.VHost{ID: "vh-1", Name: "sales", Description: "EU sales"}); err != nil {
		t.Fatalf("UpdateVHost: %v", err)
	}
	vh, _ := s.GetVHost(ctx, "vh-1")
	if vh.Description != "EU sales" {
		t.Errorf("description = %q", vh.Description)
	}
	stayed := countVHostContents(t, s, "sales")
	for _, what := range vhostContents {
		if !stayed[what] {
			t.Errorf("the %s is gone after a description change", what)
		}
	}
}

// Renaming a vhost that does not exist is ErrNotFound, not a silent no-op.
func TestRenamingAMissingVHostIsNotFound(t *testing.T) {
	s := newRotationStorage(t)
	err := s.UpdateVHost(t.Context(), storage.VHost{ID: "nope", Name: "x"})
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
