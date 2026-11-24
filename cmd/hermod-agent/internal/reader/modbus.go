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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/modbus"
	pb "github.com/gsoultan/hermod/pkg/agent/proto"
)

// Modbus defaults, as the settings document them.
const (
	defaultModbusPoll    = time.Second
	minModbusPoll        = 100 * time.Millisecond
	defaultModbusTimeout = time.Second
)

// ModbusDevice is the connection of a polled Modbus device, which commands
// write through. Polls and writes take turns on it.
type ModbusDevice struct {
	mu     sync.Mutex
	client *modbus.Client
}

// NewModbusDevice returns a device with no connection yet.
func NewModbusDevice() *ModbusDevice { return &ModbusDevice{} }

func (d *ModbusDevice) set(c *modbus.Client) {
	d.mu.Lock()
	d.client = c
	d.mu.Unlock()
}

// Write performs the write payload describes (see modbus.ParseWrite). An
// error is a WrittenError when the request was sent and the device may have
// acted on it.
func (d *ModbusDevice) Write(ctx context.Context, payload []byte) error {
	w, err := modbus.ParseWrite(payload)
	if err != nil {
		return err
	}
	d.mu.Lock()
	c := d.client
	d.mu.Unlock()
	if c == nil {
		return ErrNotConnected
	}
	err = c.Do(ctx, w)
	var ex *modbus.ExceptionError
	switch {
	case err == nil, errors.As(err, &ex):
		return err // a refusal means the device did not act on it
	default:
		return &WrittenError{Err: err}
	}
}

// RunModbus polls the device until ctx ends: it opens it, reads every block
// on each poll and records them as one JSON object, and reopens it with
// backoff after a failure. A poll the device refuses with an exception is
// skipped and the next one tried, since reconnecting would not change the
// answer.
func RunModbus(ctx context.Context, open Opener, s *pb.ModbusSettings, cfg Config, dev *ModbusDevice) {
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 30 * time.Second
	}
	wait := cfg.MinBackoff
	for ctx.Err() == nil {
		polled, err := pollOnce(ctx, open, s, &cfg, dev)
		if ctx.Err() != nil {
			return
		}
		cfg.status(StatusDisconnected, err)
		if polled {
			wait = cfg.MinBackoff
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, cfg.MaxBackoff)
	}
}

// pollOnce opens the device and polls it until a poll fails. It reports
// whether any poll succeeded.
func pollOnce(ctx context.Context, open Opener, s *pb.ModbusSettings, cfg *Config, dev *ModbusDevice) (bool, error) {
	rc, port, err := open(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = rc.Close() }()
	rw, ok := rc.(io.ReadWriter)
	if !ok {
		return false, errors.New("the Modbus connection cannot be written to")
	}
	unit := byte(s.GetUnitId())
	if unit == 0 {
		unit = 1
	}
	timeout := time.Duration(s.GetTimeoutMs()) * time.Millisecond
	if timeout <= 0 {
		timeout = defaultModbusTimeout
	}
	var client *modbus.Client
	if s.GetTransport() == pb.ModbusTransport_MODBUS_TRANSPORT_RTU {
		client = modbus.NewRTU(rw, unit, timeout)
	} else {
		client = modbus.NewTCP(rw, unit, timeout)
	}
	dev.set(client)
	defer dev.set(nil)
	cfg.status(StatusReading, nil)

	every := time.Duration(s.GetPollIntervalMs()) * time.Millisecond
	if every <= 0 {
		every = defaultModbusPoll
	}
	every = max(every, minModbusPoll)
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	polled := false
	for {
		rec, err := poll(ctx, client, s.GetReads())
		var ex *modbus.ExceptionError
		switch {
		case err == nil:
			polled = true
			emitAll(cfg, [][]byte{rec}, port)
		case errors.As(err, &ex):
			cfg.status(StatusReading, err)
		default:
			return polled, err
		}
		select {
		case <-ctx.Done():
			return polled, ctx.Err()
		case <-ticker.C:
		}
	}
}

// poll reads every block and returns them as one JSON object, keys in the
// order of the reads: registers as numbers, coils and inputs as booleans.
func poll(ctx context.Context, c *modbus.Client, reads []*pb.ModbusRead) ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, rd := range reads {
		var values any
		var err error
		addr, n := uint16(rd.GetAddress()), uint16(rd.GetCount())
		switch rd.GetTable() {
		case pb.ModbusTable_MODBUS_TABLE_COILS:
			values, err = c.ReadBits(ctx, modbus.Coils, addr, n)
		case pb.ModbusTable_MODBUS_TABLE_DISCRETE_INPUTS:
			values, err = c.ReadBits(ctx, modbus.DiscreteInputs, addr, n)
		case pb.ModbusTable_MODBUS_TABLE_HOLDING_REGISTERS:
			values, err = c.ReadRegisters(ctx, modbus.HoldingRegisters, addr, n)
		case pb.ModbusTable_MODBUS_TABLE_INPUT_REGISTERS:
			values, err = c.ReadRegisters(ctx, modbus.InputRegisters, addr, n)
		default:
			err = fmt.Errorf("read %q: unknown table", rd.GetName())
		}
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", rd.GetName(), err)
		}
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(rd.GetName())
		val, _ := json.Marshal(values)
		b.Write(key)
		b.WriteByte(':')
		b.Write(val)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}
