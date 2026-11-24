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
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/discover"
	pb "github.com/gsoultan/hermod/pkg/agent/proto"
	"google.golang.org/protobuf/proto"
)

const (
	defaultDiscoverInterval = 5 * time.Minute
	mdnsWait                = 2 * time.Second
	scanTimeout             = 500 * time.Millisecond
	modbusPort              = 502
)

// discovery looks for LAN devices on an interval and keeps the last round's
// findings for the device list.
type discovery struct {
	opt     Options
	changed func()

	mu    sync.Mutex
	found []*pb.Device
}

func newDiscovery(opt Options, changed func()) *discovery {
	if opt.MDNSAddress == "" {
		opt.MDNSAddress = discover.MDNSAddress
	}
	if opt.ScanPort == 0 {
		opt.ScanPort = modbusPort
	}
	if opt.DiscoverInterval <= 0 {
		opt.DiscoverInterval = defaultDiscoverInterval
	}
	return &discovery{opt: opt, changed: changed}
}

func (d *discovery) run(ctx context.Context) {
	if len(d.opt.MDNSServices) == 0 && len(d.opt.ScanSubnets) == 0 {
		return
	}
	t := time.NewTicker(d.opt.DiscoverInterval)
	defer t.Stop()
	for {
		d.round(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// round runs one mDNS query and one scan, and reports a change.
func (d *discovery) round(ctx context.Context) {
	var found []*pb.Device
	if len(d.opt.MDNSServices) > 0 {
		hits, err := discover.Browse(ctx, d.opt.MDNSAddress, d.opt.MDNSServices, mdnsWait)
		if err != nil {
			d.opt.Log.Warn("mDNS discovery failed", "err", err)
		}
		for _, h := range hits {
			found = append(found, &pb.Device{
				DeviceId: "mdns:" + h.Instance + "." + h.Service, Kind: pb.DeviceKind_DEVICE_KIND_TCP,
				Port: h.Addr.String(), Product: h.Instance, StableId: true, Present: true,
				DiscoveredBy: "mdns", Service: h.Service,
			})
		}
	}
	if len(d.opt.ScanSubnets) > 0 {
		open, err := discover.Scan(ctx, d.opt.ScanSubnets, d.opt.ScanPort, scanTimeout)
		if err != nil && ctx.Err() == nil {
			d.opt.Log.Warn("subnet scan failed", "err", err)
		}
		for _, ap := range open {
			found = append(found, &pb.Device{
				DeviceId: fmt.Sprintf("modbus:%s", ap), Kind: pb.DeviceKind_DEVICE_KIND_MODBUS,
				Port: ap.String(), StableId: true, Present: true, DiscoveredBy: "scan",
			})
		}
	}
	if ctx.Err() != nil {
		return
	}
	d.mu.Lock()
	same := slices.EqualFunc(d.found, found, func(a, b *pb.Device) bool { return proto.Equal(a, b) })
	d.found = found
	d.mu.Unlock()
	if !same {
		d.opt.Log.Info("LAN devices found", "count", len(found))
		d.changed()
	}
}

func (d *discovery) devices() []*pb.Device {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.found)
}
