// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// A vhost sets how long its audit log, workflow versions and decided approvals
// are kept. The hourly sweep is what applies it: each vhost by its own
// setting, and a vhost with none keeps everything, as all of them did before.

type vhostRetentionStorage struct {
	retentionStorage

	vhosts   []storage.VHost
	settings map[string]string

	purgeMu   sync.Mutex
	audits    map[string]time.Time
	versions  map[string]int
	approvals map[string]time.Time
}

func (s *vhostRetentionStorage) ListVHosts(context.Context, storage.CommonFilter) ([]storage.VHost, int, error) {
	return s.vhosts, len(s.vhosts), nil
}

func (s *vhostRetentionStorage) GetSetting(_ context.Context, key string) (string, error) {
	return s.settings[key], nil
}

func (s *vhostRetentionStorage) PurgeAuditLogsForVHost(_ context.Context, vhost string, before time.Time) error {
	s.purgeMu.Lock()
	defer s.purgeMu.Unlock()
	s.audits[vhost] = before
	return nil
}

func (s *vhostRetentionStorage) PurgeWorkflowVersions(_ context.Context, workflowID string, keep int) error {
	s.purgeMu.Lock()
	defer s.purgeMu.Unlock()
	s.versions[workflowID] = keep
	return nil
}

func (s *vhostRetentionStorage) PurgeDecidedApprovals(_ context.Context, workflowID string, before time.Time) error {
	s.purgeMu.Lock()
	defer s.purgeMu.Unlock()
	s.approvals[workflowID] = before
	return nil
}

func newVHostRetentionStorage(t *testing.T, policies map[string]storage.VHostRetention) *vhostRetentionStorage {
	t.Helper()
	s := &vhostRetentionStorage{
		settings:  map[string]string{},
		audits:    map[string]time.Time{},
		versions:  map[string]int{},
		approvals: map[string]time.Time{},
	}
	for vhost, p := range policies {
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		s.settings[storage.VHostRetentionSettingPrefix+vhost] = string(raw)
	}
	return s
}

func daysAgo(t *testing.T, cutoff time.Time) int {
	t.Helper()
	return int((time.Since(cutoff) + time.Hour) / (24 * time.Hour))
}

func TestPurgeRetention_AppliesEachVHostsOwnSetting(t *testing.T) {
	store := newVHostRetentionStorage(t, map[string]storage.VHostRetention{
		"sales":   {AuditLogDays: 90, WorkflowVersions: 20, ApprovalDays: 30},
		"default": {AuditLogDays: 365},
	})
	store.vhosts = []storage.VHost{{ID: "v1", Name: "sales"}, {ID: "v2", Name: "ops"}}
	store.workflows = []storage.Workflow{
		{ID: "wf-sales", VHost: "sales"},
		{ID: "wf-ops", VHost: "ops"},
		{ID: "wf-default", VHost: ""},
	}
	reg := newRetentionRegistry(t, &store.retentionStorage)
	reg.storage, reg.logStorage = store, store

	reg.purgeRetention()

	if cutoff, ok := store.audits["sales"]; !ok || daysAgo(t, cutoff) != 90 {
		t.Errorf("the sales audit log was purged at %v (asked: %v), want rows older than 90 days", cutoff, ok)
	}
	// "default" has no row in the vhost list, and every workflow may live in it.
	if cutoff, ok := store.audits["default"]; !ok || daysAgo(t, cutoff) != 365 {
		t.Errorf("the default vhost's audit log was purged at %v (asked: %v), want rows older than 365 days", cutoff, ok)
	}
	if _, ok := store.audits["ops"]; ok {
		t.Error("the ops audit log was purged, and ops set no retention")
	}

	if got := store.versions; len(got) != 1 || got["wf-sales"] != 20 {
		t.Errorf("versions trimmed = %v, want only wf-sales kept to 20", got)
	}
	if cutoff, ok := store.approvals["wf-sales"]; !ok || len(store.approvals) != 1 || daysAgo(t, cutoff) != 30 {
		t.Errorf("approvals purged = %v, want only wf-sales, decided over 30 days ago", store.approvals)
	}
}

func TestPurgeRetention_AVHostWithNoSettingKeepsEverything(t *testing.T) {
	store := newVHostRetentionStorage(t, nil)
	store.vhosts = []storage.VHost{{ID: "v1", Name: "sales"}}
	store.workflows = []storage.Workflow{{ID: "wf-sales", VHost: "sales"}}
	reg := newRetentionRegistry(t, &store.retentionStorage)
	reg.storage, reg.logStorage = store, store

	reg.purgeRetention()

	if len(store.audits)+len(store.versions)+len(store.approvals) != 0 {
		t.Errorf("purged audits %v, versions %v, approvals %v for a vhost that set nothing",
			store.audits, store.versions, store.approvals)
	}
}
