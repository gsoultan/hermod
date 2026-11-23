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
	"testing"

	"github.com/gsoultan/hermod"
)

func TestAgentSourceIsBuiltFromItsConfig(t *testing.T) {
	src, err := CreateSource(SourceConfig{ID: "s1", Type: "agent", VHost: "plant",
		Config: hermod.StringMap{"agent_id": "agt1", "devices": "usb:1:2:3, tcp:plc", "format": "json"}})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	_ = src.Close()
	if _, err := CreateSource(SourceConfig{ID: "s2", Type: "agent", Config: hermod.StringMap{}}); err == nil {
		t.Fatal("an agent source with no agent was built")
	}
}
