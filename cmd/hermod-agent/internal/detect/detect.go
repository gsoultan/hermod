// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package detect finds the serial devices attached to the agent's machine and
// notices when one is plugged in or out. It rescans on a timer rather than
// listening for hotplug events, which works the same on Linux and Windows
// without udev rules or drivers.
package detect

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"time"

	pb "github.com/gsoultan/hermod/pkg/agent/proto"
)

// Port is one serial port as the operating system reports it.
type Port struct {
	Name         string
	IsUSB        bool
	VID, PID     string
	SerialNumber string
	Product      string
}

// Lister lists the serial ports attached right now.
type Lister func() ([]Port, error)

// DeviceID is a port's identity. A USB device with a serial number keeps the
// same id in any socket; anything else falls back to its port path, which
// can change after a replug.
func DeviceID(p Port) (id string, stable bool) {
	if p.IsUSB && p.SerialNumber != "" {
		return "usb:" + strings.ToLower(p.VID) + ":" + strings.ToLower(p.PID) + ":" + p.SerialNumber, true
	}
	return "port:" + p.Name, false
}

// Convert describes ports for the server.
func Convert(ports []Port) []*pb.Device {
	out := make([]*pb.Device, 0, len(ports))
	for _, p := range ports {
		id, stable := DeviceID(p)
		out = append(out, &pb.Device{
			DeviceId:     id,
			Kind:         pb.DeviceKind_DEVICE_KIND_SERIAL,
			Port:         p.Name,
			Vid:          strings.ToLower(p.VID),
			Pid:          strings.ToLower(p.PID),
			SerialNumber: p.SerialNumber,
			Product:      p.Product,
			StableId:     stable,
			Present:      true,
		})
	}
	return out
}

// Watcher keeps the current list of ports.
type Watcher struct {
	list     Lister
	interval time.Duration
	onChange func([]Port)

	mu      sync.RWMutex
	scanned bool
	ports   []Port
	byID    map[string]string
}

// NewWatcher scans with list every interval and calls onChange, when set,
// with the full list whenever it differs from the last one.
func NewWatcher(list Lister, interval time.Duration, onChange func([]Port)) *Watcher {
	return &Watcher{list: list, interval: interval, onChange: onChange, byID: map[string]string{}}
}

// Scan lists the ports once. On failure the last good list is kept.
func (w *Watcher) Scan() error {
	_, err := w.scan()
	return err
}

func (w *Watcher) scan() (bool, error) {
	ports, err := w.list()
	if err != nil {
		return false, err
	}
	byID := make(map[string]string, len(ports))
	for _, p := range ports {
		id, _ := DeviceID(p)
		byID[id] = p.Name
	}
	if ports == nil {
		ports = []Port{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	// The first scan always counts as a change, so an empty machine is
	// still reported once.
	changed := !w.scanned || !reflect.DeepEqual(w.ports, ports)
	w.scanned, w.ports, w.byID = true, ports, byID
	return changed, nil
}

// Run scans until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		if changed, err := w.scan(); err == nil && changed && w.onChange != nil {
			w.onChange(w.Devices())
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Devices returns the ports from the last good scan.
func (w *Watcher) Devices() []Port {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return append([]Port(nil), w.ports...)
}

// Resolve returns the port a device is on now, and false when it is not
// attached.
func (w *Watcher) Resolve(deviceID string) (string, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	p, ok := w.byID[deviceID]
	return p, ok
}
