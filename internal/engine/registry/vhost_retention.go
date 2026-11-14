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
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// purgeVHostHistory applies each vhost's retention to its audit log, its
// workflows' saved versions and their decided approvals.
//
// These three were the tables nothing purged. A vhost that has set nothing
// keeps everything, so upgrading deletes no row until somebody asks for it.
func (r *Registry) purgeVHostHistory(ctx context.Context, store, logStore storage.Storage, workflows []storage.Workflow) {
	policies := r.vhostRetentions(ctx, store)
	if len(policies) == 0 {
		return
	}
	now := time.Now()

	r.purgeVHostAuditLogs(ctx, logStore, policies, now)

	history, ok := store.(storage.HistoryPurger)
	if !ok {
		return
	}
	for _, wf := range workflows {
		if p, ok := policies[storage.RetentionVHost(wf.VHost)]; ok {
			r.purgeWorkflowHistory(ctx, history, wf.ID, p, now)
		}
	}
}

func (r *Registry) purgeVHostAuditLogs(ctx context.Context, logStore storage.Storage, policies map[string]storage.VHostRetention, now time.Time) {
	audits, ok := logStore.(storage.VHostAuditPurger)
	if !ok {
		return
	}
	for vhost, p := range policies {
		if p.AuditLogDays <= 0 {
			continue
		}
		if err := audits.PurgeAuditLogsForVHost(ctx, vhost, now.AddDate(0, 0, -p.AuditLogDays)); err != nil {
			r.logger.Error("Registry: purging a vhost's audit log failed", "vhost", vhost, "error", err)
		}
	}
}

func (r *Registry) purgeWorkflowHistory(ctx context.Context, history storage.HistoryPurger, workflowID string, p storage.VHostRetention, now time.Time) {
	if p.WorkflowVersions > 0 {
		if err := history.PurgeWorkflowVersions(ctx, workflowID, p.WorkflowVersions); err != nil {
			r.logger.Error("Registry: trimming workflow versions failed", "workflow_id", workflowID, "error", err)
		}
	}
	if p.ApprovalDays > 0 {
		if err := history.PurgeDecidedApprovals(ctx, workflowID, now.AddDate(0, 0, -p.ApprovalDays)); err != nil {
			r.logger.Error("Registry: purging decided approvals failed", "workflow_id", workflowID, "error", err)
		}
	}
}

// vhostRetentions returns the retention of every vhost that has set one, by
// the vhost's name. "default" is read as well as the listed vhosts: it has no
// row of its own and any workflow may live in it.
func (r *Registry) vhostRetentions(ctx context.Context, store storage.Storage) map[string]storage.VHostRetention {
	names := []string{storage.RetentionVHost("")}
	list, _, err := store.ListVHosts(ctx, storage.CommonFilter{Limit: workflowPageForRetention})
	if err != nil {
		r.logger.Error("Registry: listing vhosts for retention failed", "error", err)
	}
	for _, v := range list {
		if name := storage.RetentionVHost(v.Name); name != names[0] {
			names = append(names, name)
		}
	}
	policies := make(map[string]storage.VHostRetention, len(names))
	for _, name := range names {
		p, err := storage.LoadVHostRetention(ctx, store, name)
		if err != nil {
			// Unreadable is not "keep nothing": the vhost keeps everything
			// until its setting can be read again.
			r.logger.Error("Registry: reading a vhost's retention failed, so its history is kept", "vhost", name, "error", err)
			continue
		}
		if !p.IsZero() {
			policies[name] = p
		}
	}
	return policies
}
