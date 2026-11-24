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
	run := &running{settings: proto.CloneOf(s), cancel: cancel, done: make(chan struct{})}
	run.port.Store("")
	id := s.GetDeviceId()
	cfg := reader.Config{
		Framing:  s.GetFraming(),
		Oversize: r.oversize,
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

// tcpDevices describes the claimed TCP devices for the device list. A TCP
// device is present while its reader is connected.
func (r *readers) tcpDevices() []*pb.Device {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*pb.Device
	for id, run := range r.running {
		if run.settings.GetKind() != pb.DeviceKind_DEVICE_KIND_TCP {
			continue
		}
		out = append(out, &pb.Device{
			DeviceId: id,
			Kind:     pb.DeviceKind_DEVICE_KIND_TCP,
			Port:     run.settings.GetTcp().GetAddress(),
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
	default:
		return fmt.Errorf("unknown device kind %v", s.GetKind())
	}
}

func validateTCP(t *pb.TcpSettings) error {
	host, port, err := net.SplitHostPort(t.GetAddress())
	if err != nil {
		return fmt.Errorf("address %q: %w", t.GetAddress(), err)
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
