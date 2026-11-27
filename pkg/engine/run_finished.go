// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package engine

import (
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/reply"
)

// runFinished tells a source that wants to know (hermod.RunFinisher) that the
// engine is done with m, and whether it succeeded: delivered, or run with
// nothing to deliver. It is deferred in processMessage, so it is told on every
// way out, a panic included, while m is still valid.
func (r *Runner) runFinished(m hermod.Message, rs *replyState) {
	f, ok := r.engine.currentSource().(hermod.RunFinisher)
	if !ok {
		return
	}
	f.RunFinished(m, runSucceeded(rs.status))
}

// runSucceeded reports whether a message that ended with status succeeded.
func runSucceeded(status reply.Status) bool {
	switch status {
	case reply.Delivered, reply.Completed, reply.Filtered:
		return true
	default:
		return false
	}
}
