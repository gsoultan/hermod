// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// VHostRetention is how much history a vhost keeps. Zero in a field keeps
// everything, which is what every vhost did before the setting existed.
type VHostRetention struct {
	// AuditLogDays is how long audit rows about the vhost's workflows, sources
	// and sinks are kept.
	AuditLogDays int `json:"audit_log_days"`
	// WorkflowVersions is how many of each workflow's newest versions are kept.
	WorkflowVersions int `json:"workflow_versions"`
	// ApprovalDays is how long an approved or rejected approval is kept after
	// it was decided. One still waiting is never purged.
	ApprovalDays int `json:"approval_days"`
}

// The largest values a vhost may set: ten years, and ten thousand versions.
const (
	MaxVHostRetentionDays     = 3650
	MaxVHostRetentionVersions = 10000
)

// ErrInvalidVHostRetention reports a retention setting outside its range.
var ErrInvalidVHostRetention = errors.New("invalid vhost retention")

// Validate reports the first field outside its range.
func (r VHostRetention) Validate() error {
	for _, f := range []struct {
		name       string
		value, max int
	}{
		{"audit_log_days", r.AuditLogDays, MaxVHostRetentionDays},
		{"workflow_versions", r.WorkflowVersions, MaxVHostRetentionVersions},
		{"approval_days", r.ApprovalDays, MaxVHostRetentionDays},
	} {
		if f.value < 0 || f.value > f.max {
			return fmt.Errorf("%w: %s must be between 0 and %d, got %d",
				ErrInvalidVHostRetention, f.name, f.max, f.value)
		}
	}
	return nil
}

// RetentionVHost is the name a vhost's retention and its audit rows are kept
// under. A workflow with no vhost, or the "all" the UI sends for one, lives in
// "default", which has no row in the vhost list.
func RetentionVHost(vhost string) string {
	if vhost == "" || vhost == "all" {
		return "default"
	}
	return vhost
}

// IsZero reports whether the vhost keeps everything.
func (r VHostRetention) IsZero() bool { return r == VHostRetention{} }

// settingStore is the part of Storage a vhost's retention is kept in.
type settingStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	SaveSetting(ctx context.Context, key string, value string) error
}

// VHostRetentionSettingPrefix starts the settings key a vhost's retention is
// kept under; the vhost's name follows. Renaming a vhost moves the key.
const VHostRetentionSettingPrefix = "vhost_retention:"

func vhostRetentionKey(vhost string) string {
	return VHostRetentionSettingPrefix + RetentionVHost(vhost)
}

// LoadVHostRetention returns the retention saved for a vhost, and the zero
// value, which keeps everything, when none was.
func LoadVHostRetention(ctx context.Context, s settingStore, vhost string) (VHostRetention, error) {
	raw, err := s.GetSetting(ctx, vhostRetentionKey(vhost))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return VHostRetention{}, nil
		}
		return VHostRetention{}, fmt.Errorf("reading retention of vhost %q: %w", vhost, err)
	}
	if strings.TrimSpace(raw) == "" {
		return VHostRetention{}, nil
	}
	var r VHostRetention
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return VHostRetention{}, fmt.Errorf("reading retention of vhost %q: %w", vhost, err)
	}
	return r, nil
}

// SaveVHostRetention validates and stores a vhost's retention.
func SaveVHostRetention(ctx context.Context, s settingStore, vhost string, r VHostRetention) error {
	if err := r.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := s.SaveSetting(ctx, vhostRetentionKey(vhost), string(raw)); err != nil {
		return fmt.Errorf("saving retention of vhost %q: %w", vhost, err)
	}
	return nil
}

// VHostAuditPurger is implemented by a log store that can purge one vhost's
// audit rows.
type VHostAuditPurger interface {
	PurgeAuditLogsForVHost(ctx context.Context, vhost string, before time.Time) error
}

// HistoryPurger is implemented by a store that can trim a workflow's saved
// versions and its decided approvals.
type HistoryPurger interface {
	// PurgeWorkflowVersions keeps the newest keep versions of a workflow. A keep
	// of zero or less deletes nothing.
	PurgeWorkflowVersions(ctx context.Context, workflowID string, keep int) error
	// PurgeDecidedApprovals deletes a workflow's approvals decided before the
	// time given. A pending one is never deleted.
	PurgeDecidedApprovals(ctx context.Context, workflowID string, before time.Time) error
}
