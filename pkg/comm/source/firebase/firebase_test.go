// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package firebase

import (
	"testing"
	"time"
)

func TestFirebaseSource_State(t *testing.T) {
	s := NewFirebaseSource("project", "coll", "{}", "updated_at", 1*time.Minute)

	now := time.Now().Round(time.Microsecond) // Firestore precision
	state := map[string]string{
		"last_timestamp": now.Format(time.RFC3339Nano),
	}

	s.SetState(state)

	if !s.lastTimestamp.Equal(now) {
		t.Errorf("Expected lastTimestamp %v, got %v", now, s.lastTimestamp)
	}

	newState := s.GetState()
	if newState["last_timestamp"] != state["last_timestamp"] {
		t.Errorf("Expected state last_timestamp %s, got %s", state["last_timestamp"], newState["last_timestamp"])
	}
}

func TestNewFirebaseSource(t *testing.T) {
	s := NewFirebaseSource("project", "coll", "{}", "", 0)
	if s.pollInterval != 1*time.Minute {
		t.Errorf("Expected default poll interval 1m, got %v", s.pollInterval)
	}
	if s.timestampField != "updated_at" {
		t.Errorf("Expected default timestamp field updated_at, got %s", s.timestampField)
	}
}
