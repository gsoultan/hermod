// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package agent

import (
	"context"
	"encoding/binary"
	"io"
	"sync"
	"testing"

	pb "github.com/gsoultan/hermod/pkg/agent/proto"
)

// plc is a Modbus TCP device with ten holding registers.
type plc struct {
	mu   sync.Mutex
	regs [10]uint16
}

func (p *plc) serve(t *testing.T) string {
	t.Helper()
	ln := listen(t)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				for {
					head := make([]byte, 7)
					if _, err := io.ReadFull(conn, head); err != nil {
						return
					}
					pdu := make([]byte, int(binary.BigEndian.Uint16(head[4:]))-1)
					if _, err := io.ReadFull(conn, pdu); err != nil {
						return
					}
					addr, n := binary.BigEndian.Uint16(pdu[1:]), binary.BigEndian.Uint16(pdu[3:])
					p.mu.Lock()
					resp := []byte{pdu[0] | 0x80, 1}
					switch pdu[0] {
					case 3:
						resp = []byte{3, byte(2 * n)}
						for i := range n {
							resp = binary.BigEndian.AppendUint16(resp, p.regs[addr+i])
						}
					case 6:
						p.regs[addr] = n
						resp = pdu
					}
					p.mu.Unlock()
					out := append(append([]byte(nil), head[:4]...), 0, byte(len(resp)+1), head[6])
					_, _ = conn.Write(append(out, resp...))
				}
			}()
		}
	}()
	return ln.Addr().String()
}

// A Modbus TCP device: its registers reach the server on every poll, and a
// command writes a register.
func TestAModbusDeviceIsPolledAndWritten(t *testing.T) {
	dev := &plc{}
	dev.regs[1] = 230
	addr := dev.serve(t)
	srv := &server{
		commands: make(chan *pb.Command),
		settings: &pb.DeviceSettingsList{Devices: []*pb.DeviceSettings{{
			DeviceId: "plc", Kind: pb.DeviceKind_DEVICE_KIND_MODBUS, Enabled: true, AllowCommands: true,
			Modbus: &pb.ModbusSettings{Transport: pb.ModbusTransport_MODBUS_TRANSPORT_TCP, Address: addr, PollIntervalMs: 100,
				Reads: []*pb.ModbusRead{{Name: "volts", Table: pb.ModbusTable_MODBUS_TABLE_HOLDING_REGISTERS, Address: 1, Count: 2}}},
		}}},
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, options(t.TempDir(), serve(t, srv))) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "a poll recorded", func() bool { r, _ := srv.snapshot(); return len(r) > 0 })
	recs, _ := srv.snapshot()
	if string(recs[0].GetRaw()) != `{"volts":[230,0]}` || recs[0].GetPort() != addr {
		t.Fatalf("record = %v", recs[0])
	}
	srv.commands <- &pb.Command{CommandId: "c1", DeviceId: "plc", Payload: []byte("holding 2 7")}
	eventually(t, "the write's result", func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return len(srv.results) == 1
	})
	srv.mu.Lock()
	r := srv.results[0]
	srv.mu.Unlock()
	if r.GetStatus() != pb.CommandStatus_COMMAND_STATUS_WRITTEN || !r.GetWritten() {
		t.Fatalf("result = %v", r)
	}
	eventually(t, "the new value polled", func() bool {
		recs, _ := srv.snapshot()
		return string(recs[len(recs)-1].GetRaw()) == `{"volts":[230,7]}`
	})
}
