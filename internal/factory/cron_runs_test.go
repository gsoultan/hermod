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
	"github.com/gsoultan/hermod/pkg/comm/source/cron"
)

// The engine tells a source how each run ended through hermod.RunFinisher, and
// the cron source keeps one run at a time and its "last successful run" with
// it. Every factory-built source is wrapped in a metrics decorator; one that
// did not pass the call on would leave the cron source thinking its first run
// never ended, and the schedule would fire once an hour at best. Built from a
// stored configuration, as an operator's workflow is.
func TestACronSourceFromStoredConfigurationHearsHowRunsEnded(t *testing.T) {
	src, err := CreateSource(SourceConfig{ID: "s1", Type: "cron", Config: map[string]string{
		"schedule": "@every 1m", "max_run_duration": "2h",
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if _, ok := src.(hermod.RunFinisher); !ok {
		t.Fatalf("%T does not pass on how a run ended", src)
	}
	if _, ok := src.(hermod.Stateful); !ok {
		t.Fatalf("%T does not keep the last successful run", src)
	}
	_ = cron.MetaLastSuccessAt
}
