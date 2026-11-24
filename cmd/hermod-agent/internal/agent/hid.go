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
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/hid"
	pb "github.com/gsoultan/hermod/pkg/agent/proto"
)

// hidWatcher keeps the current list of HID devices.
type hidWatcher struct {
	list     func() ([]hid.Device, error)
	interval time.Duration
	log      *slog.Logger
	changed  func()

	mu      sync.Mutex
	devices []hid.Device
}

func newHIDWatcher(list func() ([]hid.Device, error), interval time.Duration, log *slog.Logger, changed func()) *hidWatcher {
	return &hidWatcher{list: list, interval: interval, log: log, changed: changed}
}

func (w *hidWatcher) run(ctx context.Context) {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		w.scan()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (w *hidWatcher) scan() {
	found, err := w.list()
	if err != nil {
		w.log.Debug("could not list HID devices", "err", err)
		return
	}
	w.mu.Lock()
	same := slices.Equal(found, w.devices)
	w.devices = found
	w.mu.Unlock()
	if !same {
		w.changed()
	}
}

// resolve finds a device by id.
func (w *hidWatcher) resolve(id string) (hid.Device, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, d := range w.devices {
		if d.ID == id {
			return d, true
		}
	}
	return hid.Device{}, false
}

func (w *hidWatcher) pbDevices() []*pb.Device {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*pb.Device, 0, len(w.devices))
	for _, d := range w.devices {
		port := d.Event
		if port == "" {
			port = d.Hidraw
		}
		out = append(out, &pb.Device{
			DeviceId: d.ID, Kind: pb.DeviceKind_DEVICE_KIND_HID, Port: port, Vid: d.VID, Pid: d.PID,
			SerialNumber: d.Serial, Product: d.Name, StableId: d.StableID, Present: true,
		})
	}
	return out
}
