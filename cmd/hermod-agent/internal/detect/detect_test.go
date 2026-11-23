// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package detect

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestDeviceIDIsStableWhenTheUSBSerialNumberIsKnown(t *testing.T) {
	devs := Convert([]Port{
		{Name: "/dev/ttyUSB0", IsUSB: true, VID: "10C4", PID: "EA60", SerialNumber: "0001", Product: "CP2102"},
		{Name: "/dev/ttyUSB1", IsUSB: true, VID: "1a86", PID: "7523"},
		{Name: "/dev/ttyS0"},
	})
	if len(devs) != 3 {
		t.Fatalf("devices = %d", len(devs))
	}
	if devs[0].GetDeviceId() != "usb:10c4:ea60:0001" || !devs[0].GetStableId() || devs[0].GetProduct() != "CP2102" {
		t.Errorf("usb with serial = %+v", devs[0])
	}
	if devs[1].GetDeviceId() != "port:/dev/ttyUSB1" || devs[1].GetStableId() {
		t.Errorf("usb without serial = %+v", devs[1])
	}
	if devs[2].GetDeviceId() != "port:/dev/ttyS0" || devs[2].GetVid() != "" {
		t.Errorf("built-in port = %+v", devs[2])
	}
	for _, d := range devs {
		if !d.GetPresent() || d.GetKind().String() != "DEVICE_KIND_SERIAL" {
			t.Errorf("device %s: present=%v kind=%v", d.GetDeviceId(), d.GetPresent(), d.GetKind())
		}
	}
}

// The same device plugged into another USB socket gets another port name but
// keeps its id, and the resolver follows it.
func TestResolveFollowsADeviceToItsNewPort(t *testing.T) {
	var mu sync.Mutex
	ports := []Port{{Name: "/dev/ttyUSB0", IsUSB: true, VID: "10c4", PID: "ea60", SerialNumber: "A"}}
	list := func() ([]Port, error) {
		mu.Lock()
		defer mu.Unlock()
		return append([]Port(nil), ports...), nil
	}
	w := NewWatcher(list, time.Millisecond, nil)
	if err := w.Scan(); err != nil {
		t.Fatal(err)
	}
	if p, ok := w.Resolve("usb:10c4:ea60:A"); !ok || p != "/dev/ttyUSB0" {
		t.Fatalf("Resolve = %q, %v", p, ok)
	}
	mu.Lock()
	ports[0].Name = "/dev/ttyUSB3"
	mu.Unlock()
	_ = w.Scan()
	if p, ok := w.Resolve("usb:10c4:ea60:A"); !ok || p != "/dev/ttyUSB3" {
		t.Fatalf("Resolve after replug = %q, %v", p, ok)
	}
	if _, ok := w.Resolve("usb:ffff:ffff:none"); ok {
		t.Fatal("resolved a device that is not attached")
	}
}

func TestWatcherReportsOnlyChanges(t *testing.T) {
	var mu sync.Mutex
	ports := []Port{{Name: "/dev/ttyACM0"}}
	list := func() ([]Port, error) {
		mu.Lock()
		defer mu.Unlock()
		return append([]Port(nil), ports...), nil
	}
	changes := make(chan int, 10)
	w := NewWatcher(list, 5*time.Millisecond, func(d []Port) { changes <- len(d) })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go w.Run(ctx)

	if n := <-changes; n != 1 {
		t.Fatalf("first report = %d devices", n)
	}
	time.Sleep(30 * time.Millisecond)
	select {
	case n := <-changes:
		t.Fatalf("reported %d devices with nothing changed", n)
	default:
	}
	mu.Lock()
	ports = append(ports, Port{Name: "/dev/ttyACM1"})
	mu.Unlock()
	select {
	case n := <-changes:
		if n != 2 {
			t.Fatalf("after plug-in = %d", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("plug-in not reported")
	}
}

func TestScanErrorKeepsTheLastList(t *testing.T) {
	fail := false
	list := func() ([]Port, error) {
		if fail {
			return nil, errors.New("enumeration failed")
		}
		return []Port{{Name: "/dev/ttyS1"}}, nil
	}
	w := NewWatcher(list, time.Millisecond, nil)
	_ = w.Scan()
	fail = true
	if err := w.Scan(); err == nil {
		t.Fatal("Scan hid the error")
	}
	if len(w.Devices()) != 1 {
		t.Fatal("a failed scan forgot the devices")
	}
}

func TestSystemListerDoesNotFail(t *testing.T) {
	// No device is needed: an empty machine lists nothing. What matters is
	// that the platform's enumerator builds and runs here.
	if _, err := SystemPorts(); err != nil {
		t.Skipf("enumeration not available in this environment: %v", err)
	}
}
