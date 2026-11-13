// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package selfupdate

import (
	"encoding/json"
	"fmt"
)

// Mode is how updates reach this instance.
type Mode string

const (
	// ModeAutomatic checks on a schedule and installs a newer release within
	// the same major version by itself. A new major version waits for a person.
	ModeAutomatic Mode = "automatic"
	// ModeManual checks on a schedule and says when an update is available;
	// installing it is a click.
	ModeManual Mode = "manual"
	// ModeOff never checks on its own. "Check now" still works.
	ModeOff Mode = "off"
)

const (
	// SettingsKey is the settings row the choice is stored under.
	SettingsKey = "update_settings"
	// DefaultCheckIntervalHours is once a day.
	DefaultCheckIntervalHours = 24
	// MaxCheckIntervalHours is once a week.
	MaxCheckIntervalHours = 168
)

// Settings is the operator's choice of how updates arrive.
type Settings struct {
	Mode               Mode `json:"mode"`
	IncludePrereleases bool `json:"include_prereleases"`
	CheckIntervalHours int  `json:"check_interval_hours"`
}

// DefaultSettings is manual: check daily, install when someone says so. An
// unattended restart is something an operator opts into.
func DefaultSettings() Settings {
	return Settings{Mode: ModeManual, CheckIntervalHours: DefaultCheckIntervalHours}
}

// Normalize fills the defaults and refuses values that mean nothing.
func (s Settings) Normalize() (Settings, error) {
	switch s.Mode {
	case "":
		s.Mode = ModeManual
	case ModeAutomatic, ModeManual, ModeOff:
	default:
		return s, fmt.Errorf("mode must be automatic, manual or off, not %q", s.Mode)
	}
	if s.CheckIntervalHours == 0 {
		s.CheckIntervalHours = DefaultCheckIntervalHours
	}
	if s.CheckIntervalHours < 1 || s.CheckIntervalHours > MaxCheckIntervalHours {
		return s, fmt.Errorf("check_interval_hours must be between 1 and %d", MaxCheckIntervalHours)
	}
	return s, nil
}

// ParseSettings reads a stored row. Anything unreadable is the defaults: a
// damaged row must not switch automatic installs on, or off, by accident.
func ParseSettings(raw string) Settings {
	if raw == "" {
		return DefaultSettings()
	}
	var s Settings
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return DefaultSettings()
	}
	n, err := s.Normalize()
	if err != nil {
		return DefaultSettings()
	}
	return n
}
