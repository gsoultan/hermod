// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package reader

import (
	"context"
	"encoding/json"
	"io"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	pb "github.com/gsoultan/hermod/pkg/agent/proto"
)

// modbusTCPServer is a Modbus TCP device holding registers 0..9 = 100+i and
// coil 3 on. It records register writes.
func modbusTCPServer(t *testing.T) (string, *sync.Map) {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	writes := &sync.Map{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveFakeModbus(conn, writes)
		}
	}()
	return ln.Addr().String(), writes
}

func serveFakeModbus(conn net.Conn, writes *sync.Map) {
	defer conn.Close()
	for {
		head := make([]byte, 7)
		if _, err := readFull(conn, head); err != nil {
			return
		}
		pdu := make([]byte, int(head[4])<<8|int(head[5])-1)
		if _, err := readFull(conn, pdu); err != nil {
			return
		}
		addr, n := int(pdu[1])<<8|int(pdu[2]), int(pdu[3])<<8|int(pdu[4])
		var resp []byte
		switch pdu[0] {
		case 3:
			resp = []byte{3, byte(2 * n)}
			for i := range n {
				v := 100 + addr + i
				resp = append(resp, byte(v>>8), byte(v))
			}
		case 1:
			resp = []byte{1, byte((n + 7) / 8)}
			bits := make([]byte, (n+7)/8)
			for i := range n {
				if addr+i == 3 {
					bits[i/8] |= 1 << (i % 8)
				}
			}
			resp = append(resp, bits...)
		case 6:
			writes.Store(addr, n)
			resp = pdu
		default:
			resp = []byte{pdu[0] | 0x80, 1}
		}
		out := append(append([]byte(nil), head[:4]...), byte((len(resp)+1)>>8), byte(len(resp)+1), head[6])
		_, _ = conn.Write(append(out, resp...))
	}
}

func readFull(c net.Conn, b []byte) (int, error) {
	got := 0
	for got < len(b) {
		n, err := c.Read(b[got:])
		got += n
		if err != nil {
			return got, err
		}
	}
	return got, nil
}

func modbusSettings(addr string) *pb.ModbusSettings {
	return &pb.ModbusSettings{
		Transport: pb.ModbusTransport_MODBUS_TRANSPORT_TCP, Address: addr, PollIntervalMs: 100,
		Reads: []*pb.ModbusRead{
			{Name: "power", Table: pb.ModbusTable_MODBUS_TABLE_HOLDING_REGISTERS, Address: 2, Count: 2},
			{Name: "running", Table: pb.ModbusTable_MODBUS_TABLE_COILS, Address: 2, Count: 2},
		},
	}
}

// Each poll is one record: a JSON object with every read's values.
func TestRunModbusRecordsEachPoll(t *testing.T) {
	addr, _ := modbusTCPServer(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := newCollected()
	dev := NewModbusDevice()
	go RunModbus(ctx, TCPClientOpener(addr), modbusSettings(addr), fastBackoff(Config{Emit: c.emit}), dev)
	got := c.wait(t, 2)
	var rec map[string][]any
	if err := json.Unmarshal([]byte(got[0]), &rec); err != nil {
		t.Fatalf("record %q: %v", got[0], err)
	}
	if len(rec["power"]) != 2 || rec["power"][0] != float64(102) || rec["power"][1] != float64(103) ||
		rec["running"][0] != false || rec["running"][1] != true {
		t.Fatalf("record = %s", got[0])
	}
	if got[0][:9] != `{"power":` {
		t.Fatalf("keys are not in read order: %s", got[0])
	}
}

func TestModbusDeviceWritesARegister(t *testing.T) {
	addr, writes := modbusTCPServer(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dev := NewModbusDevice()
	if err := dev.Write(ctx, []byte("holding 7 1234")); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("before connecting: err = %v", err)
	}
	c := newCollected()
	go RunModbus(ctx, TCPClientOpener(addr), modbusSettings(addr), fastBackoff(Config{Emit: c.emit}), dev)
	c.wait(t, 1)
	if err := dev.Write(ctx, []byte("holding 7 1234")); err != nil {
		t.Fatal(err)
	}
	if v, ok := writes.Load(7); !ok || v != 1234 {
		t.Fatalf("register 7 = %v", v)
	}
	err := dev.Write(ctx, []byte("reboot now"))
	var we *WrittenError
	if err == nil || errors.As(err, &we) {
		t.Fatalf("a bad command: err = %v, want a not-written error", err)
	}
	// The fake refuses coil writes: refused, so not written.
	if err := dev.Write(ctx, []byte("coil 1 on")); err == nil || errors.As(err, &we) {
		t.Fatalf("a refused write: err = %v", err)
	}
}

// A device that stops answering is reconnected; polling carries on.
func TestRunModbusReconnects(t *testing.T) {
	addr, _ := modbusTCPServer(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	conns := make(chan net.Conn, 10)
	open := func(ctx context.Context) (rc io.ReadCloser, port string, err error) {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			conns <- conn
		}
		return conn, addr, err
	}
	c := newCollected()
	go RunModbus(ctx, open, modbusSettings(addr), fastBackoff(Config{Emit: c.emit}), NewModbusDevice())
	c.wait(t, 1)
	(<-conns).Close()
	select {
	case <-conns:
	case <-time.After(5 * time.Second):
		t.Fatal("no reconnect")
	}
	n := len(c.wait(t, 1))
	c.wait(t, n+1)
}
