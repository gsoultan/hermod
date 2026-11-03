// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

//go:build integration
// +build integration

package mongodb

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/security/crypto"
)

// The contract internal/storage/sql/vhost_rename_test.go holds the SQL store
// to, against a live MongoDB: a renamed vhost takes everything kept under its
// name with it, and a rename onto a taken name changes nothing.
func TestMongoRenamingAVHostCarriesEverythingItHolds(t *testing.T) {
	s, _ := newTraceMongo(t)
	ms := s.(*mongoStorage)
	t.Cleanup(func() { crypto.SetMasterKey(crypto.DefaultMasterKey) })
	crypto.SetMasterKey("vhost-rename-test-key")
	ctx := t.Context()
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	must("vhost", s.CreateVHost(ctx, storage.VHost{ID: "vh-1", Name: "sales"}))
	must("vhost", s.CreateVHost(ctx, storage.VHost{ID: "vh-2", Name: "ops"}))
	must("workflow", s.CreateWorkflow(ctx, storage.Workflow{ID: "wf-1", Name: "orders", VHost: "sales"}))
	must("source", s.CreateSource(ctx, storage.Source{ID: "src-1", Name: "pg", Type: "postgres", VHost: "sales"}))
	must("sink", s.CreateSink(ctx, storage.Sink{ID: "snk-1", Name: "kafka", Type: "kafka", VHost: "sales"}))
	must("user", s.CreateUser(ctx, storage.User{ID: "u-1", Username: "ada", Role: storage.RoleEditor, VHosts: []string{"ops", "sales"}}))
	must("secret", ms.PutVHostSecret(ctx, storage.VHostSecret{VHost: "sales", Name: "API_KEY", Value: "s3cret"}))
	must("blob", ms.PutMLBlob(ctx, "sales", "datasets/orders.csv", []byte("a,b\n1,2\n")))
	limit := int64(7)
	must("quotas", ms.PutMLQuotas(ctx, storage.MLQuotas{VHost: "sales", MaxDatasets: &limit}))
	_, err := ms.AddAIUsage(ctx, "sales", "2026-11", "wf-1", storage.AIUsageDelta{InputTokens: 10})
	must("usage", err)
	must("dashboard sample", s.RecordDashboardSample(ctx, storage.DashboardSample{VHost: "sales", Timestamp: at, TotalProcessed: 3}))

	if err := s.UpdateVHost(ctx, storage.VHost{ID: "vh-1", Name: "ops"}); !errors.Is(err, storage.ErrVHostNameTaken) {
		t.Fatalf("rename onto a taken name: err = %v, want ErrVHostNameTaken", err)
	}
	if wf, _ := s.GetWorkflow(ctx, "wf-1"); wf.VHost != "sales" {
		t.Fatalf("a failed rename moved the workflow to %q", wf.VHost)
	}

	must("rename", s.UpdateVHost(ctx, storage.VHost{ID: "vh-1", Name: "revenue", Description: "renamed"}))

	if vh, _ := s.GetVHost(ctx, "vh-1"); vh.Name != "revenue" || vh.Description != "renamed" {
		t.Errorf("vhost = %+v", vh)
	}
	if wf, _ := s.GetWorkflow(ctx, "wf-1"); wf.VHost != "revenue" {
		t.Errorf("workflow vhost = %q", wf.VHost)
	}
	if src, _ := s.GetSource(ctx, "src-1"); src.VHost != "revenue" {
		t.Errorf("source vhost = %q", src.VHost)
	}
	if snk, _ := s.GetSink(ctx, "snk-1"); snk.VHost != "revenue" {
		t.Errorf("sink vhost = %q", snk.VHost)
	}
	if u, _ := s.GetUser(ctx, "u-1"); !slices.Equal(u.VHosts, []string{"ops", "revenue"}) {
		t.Errorf("user vhosts = %v", u.VHosts)
	}
	if sec, err := ms.GetVHostSecret(ctx, "revenue", "API_KEY"); err != nil || sec.Value != "s3cret" {
		t.Errorf("secret under the new name = %q, %v", sec.Value, err)
	}
	if _, err := ms.GetVHostSecret(ctx, "sales", "API_KEY"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("secret under the old name: err = %v, want ErrNotFound", err)
	}
	if data, err := ms.GetMLBlob(ctx, "revenue", "datasets/orders.csv"); err != nil || string(data) != "a,b\n1,2\n" {
		t.Errorf("blob under the new name = %q, %v", data, err)
	}
	if q, err := ms.GetMLQuotas(ctx, "revenue"); err != nil || q.MaxDatasets == nil || *q.MaxDatasets != 7 {
		t.Errorf("quotas under the new name = %+v, %v", q, err)
	}
	if usage, err := ms.ListAIUsage(ctx, "revenue", "2026-11"); err != nil || len(usage) != 1 {
		t.Errorf("usage under the new name = %+v, %v", usage, err)
	}
	if hist, err := s.GetDashboardHistory(ctx, "revenue", time.Time{}, 10); err != nil || len(hist) != 1 {
		t.Errorf("dashboard history under the new name = %d samples, %v", len(hist), err)
	}
}
