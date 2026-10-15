// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
