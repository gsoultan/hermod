// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package agenthub

import (
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	pb "github.com/gsoultan/hermod/pkg/agent/proto"
)

func TestDeviceSettingsCarryUDPModbusAndHID(t *testing.T) {
	list := settingsList([]storage.AgentDevice{
		{DeviceID: "u", Kind: storage.AgentDeviceUDP, UDP: &storage.AgentUDP{Address: "10.0.0.1:5005", AllowFrom: []string{"10.0.0.0/8"}}},
		{DeviceID: "m", Kind: storage.AgentDeviceModbus, Serial: &storage.AgentSerial{BaudRate: 19200},
			Modbus: &storage.AgentModbus{Transport: "rtu", UnitID: 3, PollIntervalMS: 500, TimeoutMS: 200,
				Reads: []storage.AgentModbusRead{{Name: "temp", Table: "input_registers", Address: 7, Count: 2}}}},
		{DeviceID: "h", Kind: storage.AgentDeviceHID, HID: &storage.AgentHID{Mode: "raw"}},
	})
	u, m, h := list.GetDevices()[0], list.GetDevices()[1], list.GetDevices()[2]
	if u.GetKind() != pb.DeviceKind_DEVICE_KIND_UDP || u.GetUdp().GetAddress() != "10.0.0.1:5005" || u.GetUdp().GetAllowFrom()[0] != "10.0.0.0/8" {
		t.Errorf("udp = %v", u)
	}
	mb := m.GetModbus()
	if m.GetKind() != pb.DeviceKind_DEVICE_KIND_MODBUS || m.GetSerial().GetBaudRate() != 19200 ||
		mb.GetTransport() != pb.ModbusTransport_MODBUS_TRANSPORT_RTU || mb.GetUnitId() != 3 || mb.GetPollIntervalMs() != 500 ||
		mb.GetTimeoutMs() != 200 || mb.GetReads()[0].GetTable() != pb.ModbusTable_MODBUS_TABLE_INPUT_REGISTERS ||
		mb.GetReads()[0].GetAddress() != 7 || mb.GetReads()[0].GetCount() != 2 || mb.GetReads()[0].GetName() != "temp" {
		t.Errorf("modbus = %v", m)
	}
	if h.GetKind() != pb.DeviceKind_DEVICE_KIND_HID || h.GetHid().GetMode() != pb.HidMode_HID_MODE_RAW {
		t.Errorf("hid = %v", h)
	}
	found := ports([]*pb.Device{{DeviceId: "mdns:x", Kind: pb.DeviceKind_DEVICE_KIND_TCP, DiscoveredBy: "mdns", Service: "_hermod._tcp"},
		{DeviceId: "hid:1:2:3", Kind: pb.DeviceKind_DEVICE_KIND_HID}})
	if found[0].DiscoveredBy != "mdns" || found[0].Service != "_hermod._tcp" || found[1].Kind != storage.AgentDeviceHID {
		t.Errorf("ports = %+v", found)
	}
}
