// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package cdc

import (
	"context"

	"github.com/gsoultan/hermod"
)

// Connector defines the interface for Change Data Capture connectors.
type Connector interface {
	hermod.Source
	// Stream starts the CDC streaming from a specific checkpoint.
	Stream(ctx context.Context, checkpoint string) error
}
