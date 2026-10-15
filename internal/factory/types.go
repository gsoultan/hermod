// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
