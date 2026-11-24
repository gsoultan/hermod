// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package modbus is a small Modbus client for hermod-agent: the read and
// write functions a poller and a register write need, over TCP (MBAP) or RTU
// (a serial line, CRC-16). It holds no state about the device beyond the
// connection, and one request is on the wire at a time.
package modbus

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

// Function codes.
const (
	fnReadCoils              = 0x01
	fnReadDiscreteInputs     = 0x02
	fnReadHoldingRegisters   = 0x03
	fnReadInputRegisters     = 0x04
	fnWriteSingleCoil        = 0x05
	fnWriteSingleRegister    = 0x06
	fnWriteMultipleCoils     = 0x0F
	fnWriteMultipleRegisters = 0x10
)

// Table is one of the four Modbus data tables.
type Table int

const (
	Coils Table = iota + 1
	DiscreteInputs
	HoldingRegisters
	InputRegisters
)

// Protocol limits on one request.
const (
	MaxReadRegisters  = 125
	MaxReadBits       = 2000
	MaxWriteRegisters = 123
	MaxWriteBits      = 1968
)

// ErrNoAnswer is a request the device did not answer in time. It may have
// acted on it.
var ErrNoAnswer = errors.New("modbus: no answer from the device in time")

// ExceptionError is the device refusing a request.
type ExceptionError struct {
	Function byte
	Code     byte
}

func (e *ExceptionError) Error() string {
	names := map[byte]string{1: "illegal function", 2: "illegal data address", 3: "illegal data value",
		4: "server device failure", 6: "server device busy", 0x0B: "gateway target device failed to respond"}
	name := names[e.Code]
	if name == "" {
		name = "exception"
	}
	return fmt.Sprintf("modbus: function %#02x refused: %s (code %d)", e.Function, name, e.Code)
}

// Client talks to one unit over one connection.
type Client struct {
	mu      sync.Mutex
	conn    io.ReadWriter
	unit    byte
	timeout time.Duration
	rtu     bool
	txid    uint16
}

// NewTCP speaks Modbus TCP on conn to unit. Each request waits at most
// timeout for its answer.
func NewTCP(conn io.ReadWriter, unit byte, timeout time.Duration) *Client {
	return &Client{conn: conn, unit: unit, timeout: timeout}
}

// NewRTU speaks Modbus RTU on a serial line to unit.
func NewRTU(conn io.ReadWriter, unit byte, timeout time.Duration) *Client {
	return &Client{conn: conn, unit: unit, timeout: timeout, rtu: true}
}

// ReadRegisters reads holding or input registers.
func (c *Client) ReadRegisters(ctx context.Context, t Table, address, count uint16) ([]uint16, error) {
	fc := byte(fnReadHoldingRegisters)
	switch t {
	case HoldingRegisters:
	case InputRegisters:
		fc = fnReadInputRegisters
	default:
		return nil, errors.New("modbus: registers are read from holding or input registers")
	}
	if count == 0 || count > MaxReadRegisters {
		return nil, fmt.Errorf("modbus: read 1 to %d registers", MaxReadRegisters)
	}
	resp, err := c.do(ctx, request(fc, address, count))
	if err != nil {
		return nil, err
	}
	if len(resp) != 2+2*int(count) || int(resp[1]) != 2*int(count) {
		return nil, fmt.Errorf("modbus: answer has %d bytes for %d registers", len(resp), count)
	}
	out := make([]uint16, count)
	for i := range out {
		out[i] = binary.BigEndian.Uint16(resp[2+2*i:])
	}
	return out, nil
}

// ReadBits reads coils or discrete inputs.
func (c *Client) ReadBits(ctx context.Context, t Table, address, count uint16) ([]bool, error) {
	fc := byte(fnReadCoils)
	switch t {
	case Coils:
	case DiscreteInputs:
		fc = fnReadDiscreteInputs
	default:
		return nil, errors.New("modbus: bits are read from coils or discrete inputs")
	}
	if count == 0 || count > MaxReadBits {
		return nil, fmt.Errorf("modbus: read 1 to %d bits", MaxReadBits)
	}
	resp, err := c.do(ctx, request(fc, address, count))
	if err != nil {
		return nil, err
	}
	n := (int(count) + 7) / 8
	if len(resp) != 2+n || int(resp[1]) != n {
		return nil, fmt.Errorf("modbus: answer has %d bytes for %d bits", len(resp), count)
	}
	out := make([]bool, count)
	for i := range out {
		out[i] = resp[2+i/8]&(1<<(i%8)) != 0
	}
	return out, nil
}

// WriteRegisters writes holding registers from address on.
func (c *Client) WriteRegisters(ctx context.Context, address uint16, values []uint16) error {
	if len(values) == 0 || len(values) > MaxWriteRegisters {
		return fmt.Errorf("modbus: write 1 to %d registers", MaxWriteRegisters)
	}
	if len(values) == 1 {
		req := request(fnWriteSingleRegister, address, values[0])
		return c.expectEcho(ctx, req, req)
	}
	req := request(fnWriteMultipleRegisters, address, uint16(len(values)))
	req = append(req, byte(2*len(values)))
	for _, v := range values {
		req = binary.BigEndian.AppendUint16(req, v)
	}
	return c.expectEcho(ctx, req, req[:5])
}

// WriteCoils writes coils from address on.
func (c *Client) WriteCoils(ctx context.Context, address uint16, values []bool) error {
	if len(values) == 0 || len(values) > MaxWriteBits {
		return fmt.Errorf("modbus: write 1 to %d coils", MaxWriteBits)
	}
	if len(values) == 1 {
		v := uint16(0)
		if values[0] {
			v = 0xFF00
		}
		req := request(fnWriteSingleCoil, address, v)
		return c.expectEcho(ctx, req, req)
	}
	packed := make([]byte, (len(values)+7)/8)
	for i, v := range values {
		if v {
			packed[i/8] |= 1 << (i % 8)
		}
	}
	req := request(fnWriteMultipleCoils, address, uint16(len(values)))
	req = append(append(req, byte(len(packed))), packed...)
	return c.expectEcho(ctx, req, req[:5])
}

func (c *Client) expectEcho(ctx context.Context, req, want []byte) error {
	resp, err := c.do(ctx, req)
	if err != nil {
		return err
	}
	if string(resp) != string(want) {
		return fmt.Errorf("modbus: the device answered % x to a write, not % x", resp, want)
	}
	return nil
}

func request(fc byte, a, b uint16) []byte {
	return binary.BigEndian.AppendUint16(binary.BigEndian.AppendUint16([]byte{fc}, a), b)
}

// do sends one request PDU and returns the answer's PDU.
func (c *Client) do(ctx context.Context, pdu []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	deadline := time.Now().Add(c.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var resp []byte
	var err error
	if c.rtu {
		resp, err = c.doRTU(pdu, deadline)
	} else {
		resp, err = c.doTCP(pdu, deadline)
	}
	if err != nil {
		return nil, err
	}
	if len(resp) == 2 && resp[0] == pdu[0]|0x80 {
		return nil, &ExceptionError{Function: pdu[0], Code: resp[1]}
	}
	if len(resp) == 0 || resp[0] != pdu[0] {
		return nil, fmt.Errorf("modbus: answer % x does not match function %#02x", resp, pdu[0])
	}
	return resp, nil
}

func (c *Client) doTCP(pdu []byte, deadline time.Time) ([]byte, error) {
	c.txid++
	frame := binary.BigEndian.AppendUint16(nil, c.txid)
	frame = append(frame, 0, 0)
	frame = binary.BigEndian.AppendUint16(frame, uint16(len(pdu)+1))
	frame = append(append(frame, c.unit), pdu...)
	if _, err := c.conn.Write(frame); err != nil {
		return nil, fmt.Errorf("modbus: write: %w", err)
	}
	for {
		head, err := c.read(7, deadline)
		if err != nil {
			return nil, err
		}
		n := int(binary.BigEndian.Uint16(head[4:]))
		if n < 2 || n > 254 {
			return nil, fmt.Errorf("modbus: answer length %d is out of range", n)
		}
		body, err := c.read(n-1, deadline)
		if err != nil {
			return nil, err
		}
		// An answer to an earlier request that timed out: skip it.
		if binary.BigEndian.Uint16(head) != c.txid {
			continue
		}
		return body, nil
	}
}

func (c *Client) doRTU(pdu []byte, deadline time.Time) ([]byte, error) {
	if r, ok := c.conn.(interface{ ResetInputBuffer() error }); ok {
		_ = r.ResetInputBuffer() // drop a late answer to an earlier request
	}
	frame := append([]byte{c.unit}, pdu...)
	frame = binary.LittleEndian.AppendUint16(frame, crc16(frame))
	if _, err := c.conn.Write(frame); err != nil {
		return nil, fmt.Errorf("modbus: write: %w", err)
	}
	head, err := c.read(2, deadline)
	if err != nil {
		return nil, err
	}
	var rest int
	switch fc := head[1]; {
	case fc&0x80 != 0:
		rest = 3
	case fc <= fnReadInputRegisters:
		n, err := c.read(1, deadline)
		if err != nil {
			return nil, err
		}
		head = append(head, n[0])
		rest = int(n[0]) + 2
	default:
		rest = 6
	}
	tail, err := c.read(rest, deadline)
	if err != nil {
		return nil, err
	}
	whole := append(head, tail...)
	n := len(whole)
	if crc16(whole[:n-2]) != binary.LittleEndian.Uint16(whole[n-2:]) {
		return nil, errors.New("modbus: answer failed its CRC check")
	}
	if whole[0] != c.unit {
		return nil, fmt.Errorf("modbus: answer from unit %d, not %d", whole[0], c.unit)
	}
	return whole[1 : n-2], nil
}

// read reads exactly n bytes by deadline, through whichever timeout the
// connection supports: a deadline (net.Conn) or a read timeout (a serial
// port, whose Read returns no bytes when it expires).
func (c *Client) read(n int, deadline time.Time) ([]byte, error) {
	buf := make([]byte, n)
	got := 0
	for got < n {
		left := time.Until(deadline)
		if left <= 0 {
			return nil, ErrNoAnswer
		}
		switch conn := c.conn.(type) {
		case interface{ SetReadDeadline(time.Time) error }:
			_ = conn.SetReadDeadline(deadline)
		case interface{ SetReadTimeout(time.Duration) error }:
			_ = conn.SetReadTimeout(left)
		}
		k, err := c.conn.Read(buf[got:])
		got += k
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() || errors.Is(err, os.ErrDeadlineExceeded) {
				return nil, ErrNoAnswer
			}
			return nil, fmt.Errorf("modbus: read: %w", err)
		}
	}
	return buf, nil
}

// crc16 is the Modbus RTU CRC: polynomial 0xA001, initial 0xFFFF.
func crc16(b []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, x := range b {
		crc ^= uint16(x)
		for range 8 {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}
