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

func serialDevice(id string) AgentDevice {
	return AgentDevice{
		DeviceID: id, Kind: AgentDeviceSerial, Name: "scale", Enabled: true,
		Serial:  &AgentSerial{BaudRate: 9600, DataBits: 8, Parity: "none", StopBits: "1"},
		Framing: AgentFraming{Kind: "line"},
	}
}

func TestValidateAgentDevicesAcceptsWhatTheAgentCanRun(t *testing.T) {
	devices := []AgentDevice{
		serialDevice("usb:0403:6001:A1"),
		{DeviceID: "tcp:plc", Kind: AgentDeviceTCP, Name: "plc", Enabled: true,
			TCP:     &AgentTCP{Address: "10.0.0.5:502", Mode: "client"},
			Framing: AgentFraming{Kind: "delimiter", DelimiterHex: "0d0a"}},
		{DeviceID: "tcp:in", Kind: AgentDeviceTCP, Name: "in", TCP: &AgentTCP{Address: "0.0.0.0:9000", Mode: "listen"},
			Framing: AgentFraming{Kind: "length_prefix", PrefixSize: 2}},
	}
	if err := ValidateAgentDevices(devices); err != nil {
		t.Fatalf("ValidateAgentDevices: %v", err)
	}
}

func TestValidateAgentDevicesRefusesWhatTheAgentWouldRefuse(t *testing.T) {
	cases := map[string]func(*AgentDevice){
		"no device id":       func(d *AgentDevice) { d.DeviceID = "" },
		"unknown kind":       func(d *AgentDevice) { d.Kind = "bluetooth" },
		"serial without":     func(d *AgentDevice) { d.Serial = nil },
		"zero baud":          func(d *AgentDevice) { d.Serial.BaudRate = 0 },
		"bad parity":         func(d *AgentDevice) { d.Serial.Parity = "sometimes" },
		"bad stop bits":      func(d *AgentDevice) { d.Serial.StopBits = "3" },
		"bad data bits":      func(d *AgentDevice) { d.Serial.DataBits = 9 },
		"unknown framing":    func(d *AgentDevice) { d.Framing.Kind = "magic" },
		"delimiter not hex":  func(d *AgentDevice) { d.Framing = AgentFraming{Kind: "delimiter", DelimiterHex: "zz"} },
		"empty delimiter":    func(d *AgentDevice) { d.Framing = AgentFraming{Kind: "delimiter"} },
		"fixed no length":    func(d *AgentDevice) { d.Framing = AgentFraming{Kind: "fixed_length"} },
		"prefix size 3":      func(d *AgentDevice) { d.Framing = AgentFraming{Kind: "length_prefix", PrefixSize: 3} },
		"start end no end":   func(d *AgentDevice) { d.Framing = AgentFraming{Kind: "start_end", StartHex: "02"} },
		"idle no gap":        func(d *AgentDevice) { d.Framing = AgentFraming{Kind: "idle_gap"} },
		"record too large":   func(d *AgentDevice) { d.Framing.MaxRecordBytes = MaxAgentRecordBytes + 1 },
		"name too long":      func(d *AgentDevice) { d.Name = strings.Repeat("n", 129) },
		"tcp on serial kind": func(d *AgentDevice) { d.TCP = &AgentTCP{Address: "h:1", Mode: "client"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := serialDevice("usb:1:2:3")
			mutate(&d)
			if err := ValidateAgentDevices([]AgentDevice{d}); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

func TestValidateAgentDevicesRefusesTCPWithoutAHostToListenOn(t *testing.T) {
	d := AgentDevice{DeviceID: "tcp:x", Kind: AgentDeviceTCP, TCP: &AgentTCP{Address: ":9000", Mode: "listen"},
		Framing: AgentFraming{Kind: "line"}}
	if err := ValidateAgentDevices([]AgentDevice{d}); err == nil {
		t.Fatal("a listen address with no host was accepted; the agent refuses it")
	}
	d.TCP = &AgentTCP{Address: "10.0.0.1", Mode: "client"}
	if err := ValidateAgentDevices([]AgentDevice{d}); err == nil {
		t.Fatal("a client address with no port was accepted")
	}
}

func TestValidateAgentDevicesRefusesTheSameDeviceTwice(t *testing.T) {
	if err := ValidateAgentDevices([]AgentDevice{serialDevice("a"), serialDevice("a")}); err == nil {
		t.Fatal("two settings for one device were accepted")
	}
}

func TestAgentTokenNamesItsAgentAndHashesStably(t *testing.T) {
	token, hash, err := NewAgentToken("agt-1")
	if err != nil {
		t.Fatalf("NewAgentToken: %v", err)
	}
	id, ok := AgentIDFromToken(token)
	if !ok || id != "agt-1" {
		t.Fatalf("AgentIDFromToken(%q) = %q, %v", token, id, ok)
	}
	if HashAgentToken(token) != hash {
		t.Fatal("the token does not hash to the hash it was issued with")
	}
	if strings.Contains(hash, token) || len(hash) != 64 {
		t.Fatalf("hash %q is not a SHA-256 hex digest of the token", hash)
	}
	other, _, _ := NewAgentToken("agt-1")
	if other == token {
		t.Fatal("two tokens for one agent are equal")
	}
	for _, bad := range []string{"", "hag_", "nope_agt-1_x", "hag__secret", "hag_agt-1_"} {
		if _, ok := AgentIDFromToken(bad); ok {
			t.Fatalf("AgentIDFromToken(%q) accepted a malformed token", bad)
		}
	}
}
