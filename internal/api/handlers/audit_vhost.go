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

	"github.com/gsoultan/hermod/internal/storage"
)

// auditVHost names the vhost an audit row is about, which is what a vhost's
// audit retention selects rows by. Empty means the row belongs to none: a
// user, a login, or something that can no longer be found.
//
// What was saved says where it lives, so the payload is read first; that also
// covers a delete, whose row is written after the thing is gone. An action
// with no payload, such as a start or a stop, is looked up.
func (h *Handler) auditVHost(ctx context.Context, entityType, entityID, payload string) string {
	var doc struct {
		VHost *string `json:"vhost"`
		Name  string  `json:"name"`
	}
	if payload != "" {
		_ = json.Unmarshal([]byte(payload), &doc)
	}

	switch entityType {
	case "vhost":
		// A vhost's own record carries its name. Its secrets, budget and models
		// are audited with the name as the entity id.
		if doc.Name != "" {
			return doc.Name
		}
		return entityID
	case "workflow", "source", "sink":
		if doc.VHost != nil {
			return storage.RetentionVHost(*doc.VHost)
		}
		if vhost, ok := h.storedVHost(ctx, entityType, entityID); ok {
			return storage.RetentionVHost(vhost)
		}
		return ""
	case "user":
		return ""
	default:
		if doc.VHost != nil && *doc.VHost != "" {
			return storage.RetentionVHost(*doc.VHost)
		}
		return ""
	}
}

// storedVHost reads the vhost of a stored workflow, source or sink.
func (h *Handler) storedVHost(ctx context.Context, entityType, id string) (string, bool) {
	if h.Storage == nil || id == "" {
		return "", false
	}
	switch entityType {
	case "workflow":
		wf, err := h.Storage.GetWorkflow(ctx, id)
		return wf.VHost, err == nil
	case "source":
		src, err := h.Storage.GetSource(ctx, id)
		return src.VHost, err == nil
	case "sink":
		snk, err := h.Storage.GetSink(ctx, id)
		return snk.VHost, err == nil
	}
	return "", false
}
