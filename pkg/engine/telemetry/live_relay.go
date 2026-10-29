// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package telemetry

// LiveStatusReport is what a remote worker sends the platform about the
// workflows it runs. The platform's editors subscribe to status there, not on
// the worker, so without it a workflow on a remote worker drew no live edges.
type LiveStatusReport struct {
	// Running lists every workflow the worker runs, so the platform can say
	// which of them someone has open.
	Running []string `json:"running"`
	// Updates carries the status of each running workflow the platform last
	// said is open. The rest cost nothing to send.
	Updates []StatusUpdate `json:"updates,omitempty"`
}

// LiveStatusReply tells a worker which of its running workflows someone has
// open on the platform, and so whose status to send next time.
type LiveStatusReply struct {
	Watched []string `json:"watched"`
}
