// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package modbus

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// slave is an in-memory Modbus device: 100 coils and 100 holding registers;
// inputs mirror them.
type slave struct {
	mu    sync.Mutex
	coils [100]bool
	regs  [100]uint16
}

// handle answers one request PDU, as a device would.
func (s *slave) handle(pdu []byte) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	fc := pdu[0]
	addr, n := int(binary.BigEndian.Uint16(pdu[1:])), int(binary.BigEndian.Uint16(pdu[3:]))
	exc := func(code byte) []byte { return []byte{fc | 0x80, code} }
	switch fc {
	case fnReadCoils, fnReadDiscreteInputs:
		if addr+n > len(s.coils) {
			return exc(2)
		}
		out := make([]byte, (n+7)/8)
		for i := range n {
			if s.coils[addr+i] {
				out[i/8] |= 1 << (i % 8)
			}
		}
		return append([]byte{fc, byte(len(out))}, out...)
	case fnReadHoldingRegisters, fnReadInputRegisters:
		if addr+n > len(s.regs) {
			return exc(2)
		}
		out := []byte{fc, byte(2 * n)}
		for i := range n {
			out = binary.BigEndian.AppendUint16(out, s.regs[addr+i])
		}
		return out
	case fnWriteSingleCoil:
		s.coils[addr] = n == 0xFF00
		return pdu
	case fnWriteSingleRegister:
		s.regs[addr] = uint16(n)
		return pdu
	case fnWriteMultipleRegisters:
		for i := range n {
			s.regs[addr+i] = binary.BigEndian.Uint16(pdu[6+2*i:])
		}
		return pdu[:5]
	case fnWriteMultipleCoils:
		for i := range n {
			s.coils[addr+i] = pdu[6+i/8]&(1<<(i%8)) != 0
		}
		return pdu[:5]
	}
	return exc(1)
}

// serveTCP answers Modbus TCP on conn.
func (s *slave) serveTCP(conn net.Conn) {
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
		resp := s.handle(pdu)
		out := append(append([]byte(nil), head[:4]...), 0, 0, head[6])
		binary.BigEndian.PutUint16(out[4:], uint16(len(resp)+1))
		_, _ = conn.Write(append(out, resp...))
	}
}

// serveRTU answers Modbus RTU frames for unit on conn.
func (s *slave) serveRTU(conn net.Conn, unit byte) {
	defer conn.Close()
	buf := make([]byte, 512)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		frame := buf[:n]
		if frame[0] != unit || crc16(frame[:n-2]) != binary.LittleEndian.Uint16(frame[n-2:]) {
			continue
		}
		resp := append([]byte{unit}, s.handle(frame[1:n-2])...)
		_, _ = conn.Write(binary.LittleEndian.AppendUint16(resp, crc16(resp)))
	}
}

func TestCRC16MatchesTheSpecification(t *testing.T) {
	// The read request example from the Modbus over serial line guide.
	if got := crc16([]byte{0x01, 0x03, 0x00, 0x00, 0x00, 0x0A}); got != 0xCDC5 {
		t.Fatalf("crc16 = %#04x, want 0xcdc5", got)
	}
}

func tcpPair(t *testing.T) (*Client, *slave) {
	t.Helper()
	s := &slave{}
	a, b := net.Pipe()
	go s.serveTCP(b)
	t.Cleanup(func() { _ = a.Close() })
	return NewTCP(a, 1, time.Second), s
}

func TestTCPReadsRegistersAndCoils(t *testing.T) {
	c, s := tcpPair(t)
	s.regs[3], s.regs[4] = 0x1234, 65535
	s.coils[9], s.coils[11] = true, true
	regs, err := c.ReadRegisters(t.Context(), HoldingRegisters, 3, 2)
	if err != nil || len(regs) != 2 || regs[0] != 0x1234 || regs[1] != 65535 {
		t.Fatalf("ReadRegisters = %v, %v", regs, err)
	}
	bits, err := c.ReadBits(t.Context(), Coils, 9, 3)
	if err != nil || len(bits) != 3 || !bits[0] || bits[1] || !bits[2] {
		t.Fatalf("ReadBits = %v, %v", bits, err)
	}
}

func TestTCPWritesAndReportsExceptions(t *testing.T) {
	c, s := tcpPair(t)
	if err := c.WriteRegisters(t.Context(), 10, []uint16{7}); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteRegisters(t.Context(), 20, []uint16{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteCoils(t.Context(), 5, []bool{true}); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteCoils(t.Context(), 30, []bool{true, false, true}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	ok := s.regs[10] == 7 && s.regs[22] == 3 && s.coils[5] && s.coils[30] && !s.coils[31] && s.coils[32]
	s.mu.Unlock()
	if !ok {
		t.Fatal("the writes did not reach the device")
	}
	_, err := c.ReadRegisters(t.Context(), HoldingRegisters, 99, 5)
	var ex *ExceptionError
	if !errors.As(err, &ex) || ex.Code != 2 {
		t.Fatalf("err = %v, want illegal data address", err)
	}
}

func TestRTURoundTrip(t *testing.T) {
	s := &slave{}
	s.regs[0] = 42
	a, b := net.Pipe()
	go s.serveRTU(b, 7)
	defer a.Close()
	c := NewRTU(a, 7, time.Second)
	regs, err := c.ReadRegisters(t.Context(), InputRegisters, 0, 1)
	if err != nil || regs[0] != 42 {
		t.Fatalf("ReadRegisters = %v, %v", regs, err)
	}
	if err := c.WriteCoils(t.Context(), 1, []bool{true}); err != nil {
		t.Fatal(err)
	}
	bits, err := c.ReadBits(t.Context(), DiscreteInputs, 0, 2)
	if err != nil || bits[0] || !bits[1] {
		t.Fatalf("ReadBits = %v, %v", bits, err)
	}
}

// A device that never answers times out instead of hanging the poller.
func TestNoAnswerTimesOut(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go func() { _, _ = io.Copy(io.Discard, b) }()
	c := NewRTU(a, 1, 50*time.Millisecond)
	start := time.Now()
	_, err := c.ReadRegisters(context.Background(), HoldingRegisters, 0, 1)
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
	if !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("err = %v, want ErrNoAnswer", err)
	}
}

func TestParseWrite(t *testing.T) {
	cases := map[string]Write{
		"holding 40 1234":       {Table: HoldingRegisters, Address: 40, Registers: []uint16{1234}},
		"holding 0 1 2 65535\n": {Table: HoldingRegisters, Address: 0, Registers: []uint16{1, 2, 65535}},
		"coil 3 1 0 on off":     {Table: Coils, Address: 3, Bits: []bool{true, false, true, false}},
	}
	for in, want := range cases {
		got, err := ParseWrite([]byte(in))
		if err != nil || got.Table != want.Table || got.Address != want.Address ||
			!equalU16(got.Registers, want.Registers) || !equalBool(got.Bits, want.Bits) {
			t.Errorf("ParseWrite(%q) = %+v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "holding", "holding 1", "holding x 1", "holding 1 65536", "input 1 1", "coil 1 2", "holding 65535 1 2", "rm -rf /"} {
		if _, err := ParseWrite([]byte(bad)); err == nil {
			t.Errorf("ParseWrite(%q) accepted", bad)
		}
	}
}

func equalU16(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalBool(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
