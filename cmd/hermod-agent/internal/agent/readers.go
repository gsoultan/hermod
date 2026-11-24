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
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/framing"
	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/reader"
	pb "github.com/gsoultan/hermod/pkg/agent/proto"
	"google.golang.org/protobuf/proto"
)

// emitFunc receives one record from a device.
type emitFunc func(deviceID string, raw []byte, port string, at time.Time)

// readers runs one reader per enabled, claimed device and restarts a reader
// when its settings change.
type readers struct {
	ctx      context.Context
	log      *slog.Logger
	emit     emitFunc
	resolve  func(deviceID string) (string, bool)
	oversize *atomic.Uint64
	changed  func()

	mu      sync.Mutex
	running map[string]*running
}

type running struct {
	settings *pb.DeviceSettings
	cancel   context.CancelFunc
	done     chan struct{}
	status   atomic.Int32
	port     atomic.Value // string
	link     *reader.Link
}

func newReaders(ctx context.Context, log *slog.Logger, emit emitFunc, resolve func(string) (string, bool), oversize *atomic.Uint64, changed func()) *readers {
	return &readers{ctx: ctx, log: log, emit: emit, resolve: resolve, oversize: oversize, changed: changed, running: map[string]*running{}}
}

// apply makes the running readers match list: new and changed devices are
// (re)started, devices missing or disabled are stopped.
func (r *readers) apply(list *pb.DeviceSettingsList) {
	want := map[string]*pb.DeviceSettings{}
	for _, s := range list.GetDevices() {
		if !s.GetEnabled() {
			continue
		}
		if err := validate(s); err != nil {
			r.log.Warn("device settings refused", "device", s.GetDeviceId(), "err", err)
			continue
		}
		want[s.GetDeviceId()] = s
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, run := range r.running {
		if s, ok := want[id]; !ok || !proto.Equal(s, run.settings) {
			run.stop()
			delete(r.running, id)
		}
	}
	for id, s := range want {
		if _, ok := r.running[id]; !ok {
			r.running[id] = r.start(s)
		}
	}
	r.notify()
}

func (r *readers) notify() {
	if r.changed != nil {
		r.changed()
	}
}

func (run *running) stop() {
	run.cancel()
	<-run.done
}

// stopAll stops every reader and waits for them.
func (r *readers) stopAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, run := range r.running {
		run.stop()
		delete(r.running, id)
	}
}

func (r *readers) start(s *pb.DeviceSettings) *running {
	ctx, cancel := context.WithCancel(r.ctx)
	run := &running{settings: proto.CloneOf(s), cancel: cancel, done: make(chan struct{}), link: reader.NewLink()}
	run.port.Store("")
	id := s.GetDeviceId()
	cfg := reader.Config{
		Framing:  s.GetFraming(),
		Oversize: r.oversize,
		Link:     run.link,
		Emit: func(raw []byte, port string, at time.Time) {
			r.emit(id, raw, port, at)
		},
		OnStatus: func(st reader.Status, err error) {
			if run.status.Swap(int32(st)) != int32(st) {
				r.log.Info("device "+st.String(), "device", id, "name", s.GetName(), "err", err)
				r.notify()
			}
		},
	}
	go func() {
		defer close(run.done)
		r.run(ctx, s, cfg)
	}()
	return run
}

func (r *readers) run(ctx context.Context, s *pb.DeviceSettings, cfg reader.Config) {
	id := s.GetDeviceId()
	switch {
	case s.GetKind() == pb.DeviceKind_DEVICE_KIND_SERIAL:
		cfg.Open = reader.SerialOpener(func() (string, bool) { return r.resolve(id) }, s.GetSerial())
		reader.Run(ctx, cfg)
	case s.GetKind() == pb.DeviceKind_DEVICE_KIND_UDP:
		allow, _ := reader.ParseAllowList(s.GetUdp().GetAllowFrom()) // validated
		var lc net.ListenConfig
		pc, err := lc.ListenPacket(ctx, "udp", s.GetUdp().GetAddress())
		if err != nil {
			cfg.OnStatus(reader.StatusDisconnected, err)
			return
		}
		reader.RunUDP(ctx, pc, allow, cfg)
	case s.GetTcp().GetMode() == pb.TcpMode_TCP_MODE_LISTEN:
		var lc net.ListenConfig
		ln, err := lc.Listen(ctx, "tcp", s.GetTcp().GetAddress())
		if err != nil {
			cfg.OnStatus(reader.StatusDisconnected, err)
			return
		}
		reader.RunListener(ctx, ln, cfg)
	default:
		cfg.Open = reader.TCPClientOpener(s.GetTcp().GetAddress())
		reader.Run(ctx, cfg)
	}
}

// networkDevices describes the claimed LAN devices for the device list: TCP,
// UDP and Modbus TCP. One is present while its reader is reading.
func (r *readers) networkDevices() []*pb.Device {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*pb.Device
	for id, run := range r.running {
		s := run.settings
		var port string
		switch {
		case s.GetKind() == pb.DeviceKind_DEVICE_KIND_TCP:
			port = s.GetTcp().GetAddress()
		case s.GetKind() == pb.DeviceKind_DEVICE_KIND_UDP:
			port = s.GetUdp().GetAddress()
		case s.GetKind() == pb.DeviceKind_DEVICE_KIND_MODBUS && s.GetModbus().GetTransport() == pb.ModbusTransport_MODBUS_TRANSPORT_TCP:
			port = s.GetModbus().GetAddress()
		default:
			continue
		}
		out = append(out, &pb.Device{
			DeviceId: id,
			Kind:     s.GetKind(),
			Port:     port,
			StableId: true,
			Present:  reader.Status(run.status.Load()) == reader.StatusReading,
		})
	}
	return out
}

// validate refuses settings a reader could not use, so one bad device does
// not stop the others.
func validate(s *pb.DeviceSettings) error {
	if s.GetDeviceId() == "" {
		return errors.New("no device id")
	}
	if _, err := framing.New(s.GetFraming()); err != nil {
		return err
	}
	switch s.GetKind() {
	case pb.DeviceKind_DEVICE_KIND_SERIAL:
		if s.GetSerial().GetBaudRate() == 0 {
			return errors.New("serial device needs a baud rate")
		}
		if d := s.GetSerial().GetDataBits(); d != 0 && (d < 5 || d > 8) {
			return fmt.Errorf("data bits %d: must be 5 to 8", d)
		}
		return nil
	case pb.DeviceKind_DEVICE_KIND_TCP:
		return validateTCP(s.GetTcp())
	case pb.DeviceKind_DEVICE_KIND_UDP:
		if s.GetUdp() == nil {
			return errors.New("UDP device needs an address")
		}
		if _, err := reader.ParseAllowList(s.GetUdp().GetAllowFrom()); err != nil {
			return err
		}
		return validateHostPort(s.GetUdp().GetAddress())
	case pb.DeviceKind_DEVICE_KIND_MODBUS:
		return validateModbus(s)
	case pb.DeviceKind_DEVICE_KIND_HID:
		if s.GetHid() == nil {
			return errors.New("HID device needs its HID settings")
		}
		return nil
	default:
		return fmt.Errorf("unknown device kind %v", s.GetKind())
	}
}

func validateTCP(t *pb.TcpSettings) error {
	return validateHostPort(t.GetAddress())
}

// Modbus limits from the protocol.
const (
	maxModbusRegisters = 125
	maxModbusBits      = 2000
	maxModbusUnit      = 247
)

func validateModbus(s *pb.DeviceSettings) error {
	m := s.GetModbus()
	if m == nil {
		return errors.New("modbus device needs its Modbus settings")
	}
	switch m.GetTransport() {
	case pb.ModbusTransport_MODBUS_TRANSPORT_TCP:
		if err := validateHostPort(m.GetAddress()); err != nil {
			return err
		}
	case pb.ModbusTransport_MODBUS_TRANSPORT_RTU:
		if s.GetSerial().GetBaudRate() == 0 {
			return errors.New("modbus RTU device needs a baud rate")
		}
	default:
		return fmt.Errorf("unknown Modbus transport %v", m.GetTransport())
	}
	if m.GetUnitId() > maxModbusUnit {
		return fmt.Errorf("unit id %d: must be 0 to %d", m.GetUnitId(), maxModbusUnit)
	}
	if len(m.GetReads()) == 0 {
		return errors.New("modbus device has nothing to read")
	}
	for _, rd := range m.GetReads() {
		limit := uint32(maxModbusRegisters)
		switch rd.GetTable() {
		case pb.ModbusTable_MODBUS_TABLE_COILS, pb.ModbusTable_MODBUS_TABLE_DISCRETE_INPUTS:
			limit = maxModbusBits
		case pb.ModbusTable_MODBUS_TABLE_HOLDING_REGISTERS, pb.ModbusTable_MODBUS_TABLE_INPUT_REGISTERS:
		default:
			return fmt.Errorf("read %q: unknown table %v", rd.GetName(), rd.GetTable())
		}
		if rd.GetName() == "" || rd.GetCount() == 0 || rd.GetCount() > limit || rd.GetAddress()+rd.GetCount() > 65536 {
			return fmt.Errorf("read %q: needs a name and 1 to %d values within addresses 0 to 65535", rd.GetName(), limit)
		}
	}
	return nil
}

func validateHostPort(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("address %q: %w", address, err)
	}
	if port == "" {
		return errors.New("address has no port")
	}
	// An empty host would listen on every interface. The user has to name
	// one, even if it is 0.0.0.0, so that is never an accident.
	if host == "" {
		return errors.New("address needs a host")
	}
	return nil
}
