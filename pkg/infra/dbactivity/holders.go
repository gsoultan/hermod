// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package dbactivity

import (
	"time"

	"github.com/gsoultan/hermod"
)

// Holders is qs as a busy answer carries them (hermod.Busy.Holders), so a run
// can show what a step waited behind: workflow, step, kind and how long each
// had been running at now. Query text is never included; it can hold personal
// data.
func Holders(qs []Query, now time.Time) []hermod.Holder {
	if len(qs) == 0 {
		return nil
	}
	out := make([]hermod.Holder, len(qs))
	for i, q := range qs {
		out[i] = hermod.Holder{WorkflowID: q.WorkflowID, NodeID: q.NodeID, Kind: string(q.Kind), Running: now.Sub(q.Started)}
	}
	return out
}
