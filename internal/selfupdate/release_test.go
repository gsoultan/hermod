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
	"testing"
)

func TestCanonical(t *testing.T) {
	cases := map[string]string{
		"v1.24.9 (Enterprise Edition)":                "v1.24.9",
		"v1.25.0-rc.1 (Enterprise Edition)":           "v1.25.0-rc.1",
		"1.24.9":                                      "v1.24.9",
		"dev (Enterprise Edition)":                    "",
		"v0.0.0-20260101-abcdef (Enterprise Edition)": "v0.0.0-20260101-abcdef",
		"":     "",
		"v1.2": "",
	}
	for in, want := range cases {
		if got := Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPickNewest(t *testing.T) {
	rels := []Release{
		{Tag: "v1.24.8"},
		{Tag: "v1.25.0-rc.1", Prerelease: true},
		{Tag: "v1.24.10"},
		{Tag: "v2.0.0", Draft: true},
		{Tag: "not-a-version"},
	}
	cases := []struct {
		name    string
		current string
		pre     bool
		wantTag string
	}{
		{"stable channel skips prereleases and drafts", "v1.24.9 (Enterprise Edition)", false, "v1.24.10"},
		{"prerelease channel offers the rc", "v1.24.9", true, "v1.25.0-rc.1"},
		{"already newest", "v1.24.10", false, ""},
		{"never offers a downgrade", "v1.30.0", true, ""},
		{"numeric not lexical order", "v1.24.9", false, "v1.24.10"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Pick(rels, c.current, c.pre)
			gotTag := ""
			if got != nil {
				gotTag = got.Tag
			}
			if gotTag != c.wantTag {
				t.Fatalf("Pick = %q, want %q", gotTag, c.wantTag)
			}
		})
	}
}

func TestPickWithUnknownCurrentOffersNothing(t *testing.T) {
	// A development build has no version to compare against; offering a
	// release would replace a binary somebody built on purpose.
	if got := Pick([]Release{{Tag: "v9.9.9"}}, "dev (Enterprise Edition)", true); got != nil {
		t.Fatalf("Pick on a dev build = %q, want nothing", got.Tag)
	}
}

func TestSameMajor(t *testing.T) {
	if !SameMajor("v1.2.3", "v1.9.0 (Enterprise Edition)") {
		t.Error("v1.2.3 and v1.9.0 share a major")
	}
	if SameMajor("v1.9.0", "v2.0.0") {
		t.Error("v1 and v2 do not share a major")
	}
}

func TestSettingsNormalize(t *testing.T) {
	s, err := Settings{}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if s.Mode != ModeManual || s.CheckIntervalHours != DefaultCheckIntervalHours {
		t.Fatalf("zero settings normalised to %+v, want manual every %dh", s, DefaultCheckIntervalHours)
	}
	if _, err := (Settings{Mode: "sometimes"}).Normalize(); err == nil {
		t.Error("an unknown mode was accepted")
	}
	if _, err := (Settings{Mode: ModeAutomatic, CheckIntervalHours: 0}).Normalize(); err != nil {
		t.Errorf("automatic with default interval refused: %v", err)
	}
	for _, h := range []int{-1, MaxCheckIntervalHours + 1} {
		if _, err := (Settings{Mode: ModeAutomatic, CheckIntervalHours: h}).Normalize(); err == nil {
			t.Errorf("interval %dh was accepted", h)
		}
	}
}

func TestParseSettingsFallsBackToDefaults(t *testing.T) {
	for _, raw := range []string{"", "{", `{"mode":"bogus"}`} {
		if got := ParseSettings(raw); got != DefaultSettings() {
			t.Errorf("ParseSettings(%q) = %+v, want defaults", raw, got)
		}
	}
	got := ParseSettings(`{"mode":"automatic","include_prereleases":true,"check_interval_hours":6}`)
	if got.Mode != ModeAutomatic || !got.IncludePrereleases || got.CheckIntervalHours != 6 {
		t.Fatalf("ParseSettings = %+v", got)
	}
}
