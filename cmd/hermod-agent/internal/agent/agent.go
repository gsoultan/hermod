// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package agent wires hermod-agent together: it finds devices, reads the
// claimed ones into the spool, and streams the spool to the Hermod server,
// which in turn says which devices to read and how.
package agent

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/detect"
	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/spool"
	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/uplink"
	pb "github.com/gsoultan/hermod/pkg/agent/proto"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/proto"
)

// settingsFile caches the last settings the server sent, so after a restart
// the agent reads its devices even while the server is unreachable.
const settingsFile = "settings.pb"

// Options configure an agent.
type Options struct {
	DataDir string
	Server  string
	Token   string
	Creds   credentials.TransportCredentials
	Version string
	Spool   spool.Options
	Log     *slog.Logger

	// ScanInterval is how often serial ports are listed; default 2 s.
	ScanInterval time.Duration
	// Lister lists serial ports; default the operating system's.
	Lister detect.Lister
	// MDNSServices are the mDNS service types to look for, such as
	// "_modbus._tcp"; none turns mDNS discovery off. MDNSAddress is where
	// queries go, default the mDNS group (tests point it elsewhere).
	MDNSServices []string
	MDNSAddress  string
	// ScanSubnets, when set, are scanned for ScanPort (default 502, Modbus
	// TCP). Off unless the operator names them.
	ScanSubnets []netip.Prefix
	ScanPort    uint16
	// DiscoverInterval is how often both run; default 5 minutes.
	DiscoverInterval time.Duration

	// Dialer and uplink backoff are for tests.
	Dialer                 func(ctx context.Context, addr string) (net.Conn, error)
	MinBackoff, MaxBackoff time.Duration
}

// Run runs the agent until ctx ends.
func Run(ctx context.Context, opt Options) error {
	if opt.Log == nil {
		opt.Log = slog.Default()
	}
	if opt.Lister == nil {
		opt.Lister = detect.SystemPorts
	}
	if opt.ScanInterval <= 0 {
		opt.ScanInterval = 2 * time.Second
	}
	if err := os.MkdirAll(opt.DataDir, 0o700); err != nil {
		return err
	}
	sp, err := spool.Open(filepath.Join(opt.DataDir, "spool"), opt.Spool)
	if err != nil {
		return err
	}
	defer func() { _ = sp.Close() }()

	a := newAgent(ctx, opt, sp)
	var wg sync.WaitGroup
	wg.Go(func() { a.batch.run(ctx) })
	wg.Go(func() { a.watcher.Run(ctx) })
	wg.Go(func() { a.discovery.run(ctx) })
	if cached, err := loadSettings(opt.DataDir); err != nil {
		opt.Log.Warn("cached device settings unreadable; waiting for the server", "err", err)
	} else if cached != nil {
		a.readers.apply(cached)
	}
	err = uplink.Run(ctx, a.uplinkConfig())
	a.readers.stopAll()
	wg.Wait()
	return err
}

type agent struct {
	opt       Options
	sp        *spool.Spool
	batch     *batcher
	watcher   *detect.Watcher
	readers   *readers
	discovery *discovery
	oversize  atomic.Uint64
	changed   chan struct{}
}

func newAgent(ctx context.Context, opt Options, sp *spool.Spool) *agent {
	a := &agent{opt: opt, sp: sp, batch: newBatcher(sp, opt.Log), changed: make(chan struct{}, 1)}
	a.watcher = detect.NewWatcher(opt.Lister, opt.ScanInterval, func([]detect.Port) { a.devicesChanged() })
	emit := func(deviceID string, raw []byte, port string, at time.Time) {
		a.batch.add(ctx, spool.Record{DeviceID: deviceID, Port: port, ReceivedAt: at.UnixNano(), Raw: raw})
	}
	a.readers = newReaders(ctx, opt.Log, emit, a.watcher.Resolve, &a.oversize, a.devicesChanged)
	a.discovery = newDiscovery(opt, a.devicesChanged)
	return a
}

func (a *agent) devicesChanged() {
	select {
	case a.changed <- struct{}{}:
	default:
	}
}

func (a *agent) devices() []*pb.Device {
	network := a.readers.networkDevices()
	claimed := map[string]bool{}
	for _, d := range network {
		claimed[d.GetPort()] = true
	}
	out := append(detect.Convert(a.watcher.Devices()), network...)
	// A found device already claimed by its address is listed once.
	for _, d := range a.discovery.devices() {
		if !claimed[d.GetPort()] {
			out = append(out, d)
		}
	}
	return out
}

func (a *agent) uplinkConfig() uplink.Config {
	return uplink.Config{
		Address:        a.opt.Server,
		Token:          a.opt.Token,
		Creds:          a.opt.Creds,
		Dialer:         a.opt.Dialer,
		Spool:          a.sp,
		Hello:          a.hello,
		Devices:        a.devices,
		DevicesChanged: a.changed,
		OnSettings:     a.onSettings,
		OnCommand:      a.readers.command,
		Heartbeat:      func(hb *pb.Heartbeat) { hb.OversizeDropped = a.oversize.Load() },
		OnState:        a.onState,
		MinBackoff:     a.opt.MinBackoff,
		MaxBackoff:     a.opt.MaxBackoff,
	}
}

func (a *agent) hello() *pb.Hello {
	host, _ := os.Hostname()
	return &pb.Hello{Version: a.opt.Version, Hostname: host, Os: runtime.GOOS, Arch: runtime.GOARCH}
}

func (a *agent) onSettings(list *pb.DeviceSettingsList) {
	if err := saveSettings(a.opt.DataDir, list); err != nil {
		a.opt.Log.Warn("could not cache device settings", "err", err)
	}
	a.readers.apply(list)
}

func (a *agent) onState(connected bool, err error) {
	if connected {
		a.opt.Log.Info("connected to Hermod", "server", a.opt.Server)
		return
	}
	a.opt.Log.Warn("not connected to Hermod; readings stay in the spool", "server", a.opt.Server, "err", err)
}

func loadSettings(dir string) (*pb.DeviceSettingsList, error) {
	b, err := os.ReadFile(filepath.Join(dir, settingsFile)) //nolint:gosec // G304: the operator's own -data-dir
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	list := &pb.DeviceSettingsList{}
	return list, proto.Unmarshal(b, list)
}

func saveSettings(dir string, list *pb.DeviceSettingsList) error {
	b, err := proto.Marshal(list)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, settingsFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, settingsFile))
}
