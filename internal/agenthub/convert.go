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
	"encoding/hex"

	"github.com/gsoultan/hermod/internal/storage"
	pb "github.com/gsoultan/hermod/pkg/agent/proto"
)

// settingsList is what the agent is sent: every claimed device, enabled or
// not, so it stops reading one that was disabled.
func settingsList(devices []storage.AgentDevice) *pb.DeviceSettingsList {
	out := &pb.DeviceSettingsList{Devices: make([]*pb.DeviceSettings, 0, len(devices))}
	for _, d := range devices {
		out.Devices = append(out.Devices, deviceSettings(d))
	}
	return out
}

func deviceSettings(d storage.AgentDevice) *pb.DeviceSettings {
	s := &pb.DeviceSettings{
		DeviceId: d.DeviceID, Kind: deviceKind(d.Kind), Name: d.Name, Enabled: d.Enabled,
		AllowCommands: d.AllowCommands,
		Framing:       framing(d.Framing),
	}
	if d.Serial != nil {
		s.Serial = &pb.SerialSettings{
			BaudRate: uint32(d.Serial.BaudRate), DataBits: uint32(d.Serial.DataBits),
			Parity: parities[d.Serial.Parity], StopBits: stopBits[d.Serial.StopBits],
		}
	}
	if d.TCP != nil {
		s.Tcp = &pb.TcpSettings{Address: d.TCP.Address, Mode: tcpModes[d.TCP.Mode]}
	}
	if d.UDP != nil {
		s.Udp = &pb.UdpSettings{Address: d.UDP.Address, AllowFrom: d.UDP.AllowFrom}
	}
	if m := d.Modbus; m != nil {
		s.Modbus = &pb.ModbusSettings{
			Transport: modbusTransports[m.Transport], Address: m.Address, UnitId: uint32(m.UnitID),
			PollIntervalMs: uint32(m.PollIntervalMS), TimeoutMs: uint32(m.TimeoutMS),
		}
		for _, r := range m.Reads {
			s.Modbus.Reads = append(s.Modbus.Reads, &pb.ModbusRead{
				Name: r.Name, Table: modbusTables[r.Table], Address: uint32(r.Address), Count: uint32(r.Count),
			})
		}
	}
	if d.HID != nil {
		s.Hid = &pb.HidSettings{Mode: hidModes[d.HID.Mode]}
	}
	return s
}

var (
	parities = map[string]pb.Parity{
		"none": pb.Parity_PARITY_NONE, "odd": pb.Parity_PARITY_ODD, "even": pb.Parity_PARITY_EVEN,
		"mark": pb.Parity_PARITY_MARK, "space": pb.Parity_PARITY_SPACE,
	}
	stopBits = map[string]pb.StopBits{
		"1": pb.StopBits_STOP_BITS_ONE, "1.5": pb.StopBits_STOP_BITS_ONE_POINT_FIVE, "2": pb.StopBits_STOP_BITS_TWO,
	}
	tcpModes = map[string]pb.TcpMode{
		"client": pb.TcpMode_TCP_MODE_CLIENT, "listen": pb.TcpMode_TCP_MODE_LISTEN,
	}
	modbusTransports = map[string]pb.ModbusTransport{
		"tcp": pb.ModbusTransport_MODBUS_TRANSPORT_TCP, "rtu": pb.ModbusTransport_MODBUS_TRANSPORT_RTU,
	}
	modbusTables = map[string]pb.ModbusTable{
		"coils":             pb.ModbusTable_MODBUS_TABLE_COILS,
		"discrete_inputs":   pb.ModbusTable_MODBUS_TABLE_DISCRETE_INPUTS,
		"holding_registers": pb.ModbusTable_MODBUS_TABLE_HOLDING_REGISTERS,
		"input_registers":   pb.ModbusTable_MODBUS_TABLE_INPUT_REGISTERS,
	}
	hidModes = map[string]pb.HidMode{
		"keyboard": pb.HidMode_HID_MODE_KEYBOARD, "raw": pb.HidMode_HID_MODE_RAW,
	}
	framingKinds = map[string]pb.FramingKind{
		storage.AgentFramingLine:         pb.FramingKind_FRAMING_KIND_LINE,
		storage.AgentFramingDelimiter:    pb.FramingKind_FRAMING_KIND_DELIMITER,
		storage.AgentFramingFixedLength:  pb.FramingKind_FRAMING_KIND_FIXED_LENGTH,
		storage.AgentFramingStartEnd:     pb.FramingKind_FRAMING_KIND_START_END,
		storage.AgentFramingLengthPrefix: pb.FramingKind_FRAMING_KIND_LENGTH_PREFIX,
		storage.AgentFramingIdleGap:      pb.FramingKind_FRAMING_KIND_IDLE_GAP,
	}
)

var deviceKinds = map[string]pb.DeviceKind{
	storage.AgentDeviceSerial: pb.DeviceKind_DEVICE_KIND_SERIAL,
	storage.AgentDeviceTCP:    pb.DeviceKind_DEVICE_KIND_TCP,
	storage.AgentDeviceUDP:    pb.DeviceKind_DEVICE_KIND_UDP,
	storage.AgentDeviceModbus: pb.DeviceKind_DEVICE_KIND_MODBUS,
	storage.AgentDeviceHID:    pb.DeviceKind_DEVICE_KIND_HID,
}

func deviceKind(kind string) pb.DeviceKind { return deviceKinds[kind] }

func kindName(kind pb.DeviceKind) string {
	for name, k := range deviceKinds {
		if k == kind {
			return name
		}
	}
	return ""
}

// framing converts settings that were validated when they were saved, so the
// hex has already been checked.
func framing(f storage.AgentFraming) *pb.Framing {
	unhex := func(s string) []byte {
		b, _ := hex.DecodeString(s)
		return b
	}
	return &pb.Framing{
		Kind: framingKinds[f.Kind], Delimiter: unhex(f.DelimiterHex), Length: uint32(f.Length),
		StartBytes: unhex(f.StartHex), EndBytes: unhex(f.EndHex), PrefixSize: uint32(f.PrefixSize),
		IdleMs: uint32(f.IdleMS), MaxRecordBytes: uint32(f.MaxRecordBytes),
	}
}

func ports(list []*pb.Device) []storage.AgentPort {
	out := make([]storage.AgentPort, 0, len(list))
	for _, d := range list {
		out = append(out, storage.AgentPort{
			DeviceID: d.GetDeviceId(), Kind: kindName(d.GetKind()), Port: d.GetPort(), VID: d.GetVid(),
			PID: d.GetPid(), SerialNumber: d.GetSerialNumber(), Product: d.GetProduct(),
			StableID: d.GetStableId(), Present: d.GetPresent(),
			DiscoveredBy: d.GetDiscoveredBy(), Service: d.GetService(),
		})
	}
	return out
}
