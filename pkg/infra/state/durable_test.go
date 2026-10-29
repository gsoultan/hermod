// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package state

import (
	"path/filepath"
	"testing"
)

// A transactional group refuses a state store that forgets on restart: its
// coordinator's log is what resolves a transaction left in doubt by a crash.
// "memory" is SQLite's :memory:, so the store has to say which it is.
func TestSQLiteStateStoreReportsWhetherItSurvivesARestart(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"memory", Config{Type: "memory"}, false},
		{"sqlite file", Config{Type: "sqlite", Path: filepath.Join(t.TempDir(), "state.db")}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ss, err := NewStateStore(c.cfg)
			if err != nil {
				t.Fatalf("NewStateStore: %v", err)
			}
			d, ok := ss.(interface{ Durable() bool })
			if !ok {
				t.Fatalf("%T does not report its durability", ss)
			}
			if got := d.Durable(); got != c.want {
				t.Errorf("Durable() = %v, want %v", got, c.want)
			}
		})
	}
}
