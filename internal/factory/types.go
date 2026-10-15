// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package factory

import (
	"time"

	"github.com/gsoultan/hermod"
)

type SourceConfig struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// VHost is the vhost the source belongs to: a `secret:NAME` in its config
	// is resolved from that vhost's secrets first.
	VHost              string           `json:"vhost,omitempty"`
	Config             hermod.StringMap `json:"config"`
	State              hermod.StringMap `json:"state"`
	ReconnectIntervals []time.Duration  `json:"-"`
}

type SinkConfig struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// VHost is the vhost the sink belongs to; see SourceConfig.VHost.
	VHost  string           `json:"vhost,omitempty"`
	Config hermod.StringMap `json:"config"`
}
