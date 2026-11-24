// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package storage

import (
	"strings"
	"testing"
)

func withCommands(cmds ...AgentCommand) AgentDevice {
	d := serialDevice("usb:1:2:3")
	d.AllowCommands = true
	d.Commands = cmds
	return d
}

func TestValidateAgentDevicesAcceptsCommandTemplates(t *testing.T) {
	d := withCommands(
		AgentCommand{Name: "tare", Payload: `T\r\n`},
		AgentCommand{Name: "relay_on", Payload: "a00101a2", Encoding: AgentCommandHex},
		AgentCommand{Name: "set-target", Payload: `S {{target}}\r\n`, ReplyTimeoutMS: 2000},
	)
	if err := ValidateAgentDevices([]AgentDevice{d}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAgentDevicesRefusesBadCommandTemplates(t *testing.T) {
	cases := map[string]AgentCommand{
		"needs a name":         {Payload: "T"},
		"name":                 {Name: "has space", Payload: "T"},
		"empty":                {Name: "e"},
		"hex":                  {Name: "h", Payload: "zz", Encoding: AgentCommandHex},
		"placeholders":         {Name: "h", Payload: "{{x}}", Encoding: AgentCommandHex},
		"encoding":             {Name: "u", Payload: "T", Encoding: "utf16"},
		"reply":                {Name: "r", Payload: "T", ReplyTimeoutMS: 60001},
		"64 KiB":               {Name: "b", Payload: strings.Repeat("x", 64<<10+1)},
		"placeholder name":     {Name: "p", Payload: "{{bad name}}"},
		"unclosed placeholder": {Name: "p", Payload: "S {{target"},
		"escape":               {Name: "p", Payload: `S \q`},
	}
	for want, c := range cases {
		err := ValidateAgentDevices([]AgentDevice{withCommands(c)})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: err = %v, want one mentioning %q", c, err, want)
		}
	}
	dup := withCommands(AgentCommand{Name: "t", Payload: "T"}, AgentCommand{Name: "t", Payload: "U"})
	if err := ValidateAgentDevices([]AgentDevice{dup}); err == nil {
		t.Error("two commands with one name accepted")
	}
}

func TestRenderAgentCommand(t *testing.T) {
	got, err := RenderAgentCommand(AgentCommand{Name: "s", Payload: `S {{target}} {{unit}}\r\n`},
		map[string]any{"target": 12.5, "unit": "kg"})
	if err != nil || string(got) != "S 12.5 kg\r\n" {
		t.Fatalf("got %q, %v", got, err)
	}
	got, err = RenderAgentCommand(AgentCommand{Name: "h", Payload: "A0 01 01 A2", Encoding: AgentCommandHex}, nil)
	if err != nil || string(got) != "\xa0\x01\x01\xa2" {
		t.Fatalf("hex: got %q, %v", got, err)
	}
	got, err = RenderAgentCommand(AgentCommand{Name: "x", Payload: `\x02W\x03\\`}, nil)
	if err != nil || string(got) != "\x02W\x03\\" {
		t.Fatalf("escapes: got %q, %v", got, err)
	}
}

// A value from a message fills a placeholder and nothing more: it cannot
// add a line ending that would make the device run a second command.
func TestRenderAgentCommandRefusesUnsafeValues(t *testing.T) {
	cmd := AgentCommand{Name: "s", Payload: `S {{target}}\r\n`}
	for _, v := range []any{"1\r\nT", "\x00", strings.Repeat("9", 257), `\r\n`[:0] + "a\tb"} {
		if _, err := RenderAgentCommand(cmd, map[string]any{"target": v}); err == nil {
			t.Errorf("value %q accepted", v)
		}
	}
	if _, err := RenderAgentCommand(cmd, map[string]any{}); err == nil || !strings.Contains(err.Error(), "target") {
		t.Errorf("missing value: err = %v", err)
	}
	// The escape is decoded in the template only, never in a value.
	got, err := RenderAgentCommand(cmd, map[string]any{"target": `\r`})
	if err != nil || string(got) != "S \\r\r\n" {
		t.Errorf("escaped value: got %q, %v", got, err)
	}
}

func TestFindAgentCommand(t *testing.T) {
	a := Agent{Devices: []AgentDevice{withCommands(AgentCommand{Name: "tare", Payload: "T"})}}
	if _, _, err := a.FindCommand("usb:1:2:3", "tare"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.FindCommand("usb:1:2:3", "nope"); err == nil {
		t.Error("unknown command found")
	}
	a.Devices[0].AllowCommands = false
	if _, _, err := a.FindCommand("usb:1:2:3", "tare"); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("command on a device without Allow commands: %v", err)
	}
	a.Devices[0].AllowCommands, a.Devices[0].Enabled = true, false
	if _, _, err := a.FindCommand("usb:1:2:3", "tare"); err == nil {
		t.Error("command on a disabled device found")
	}
}
