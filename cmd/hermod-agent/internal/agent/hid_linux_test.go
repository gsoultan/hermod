// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

//go:build linux

package agent

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/hid"
	pb "github.com/gsoultan/hermod/pkg/agent/proto"
)

// A HID device in raw mode: it is listed, and each report it sends reaches
// the server as one record. A FIFO stands in for /dev/hidraw0.
func TestARawHIDDeviceIsListedAndRead(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "hidraw0")
	if err := syscall.Mkfifo(node, 0o600); err != nil {
		t.Skip("no FIFOs here:", err)
	}
	srv := &server{settings: &pb.DeviceSettingsList{Devices: []*pb.DeviceSettings{{
		DeviceId: "hid:05e0:1200:S1", Kind: pb.DeviceKind_DEVICE_KIND_HID, Enabled: true,
		Hid: &pb.HidSettings{Mode: pb.HidMode_HID_MODE_RAW},
	}}}}
	opt := options(t.TempDir(), serve(t, srv))
	opt.HIDLister = func() ([]hid.Device, error) {
		return []hid.Device{{ID: "hid:05e0:1200:S1", StableID: true, VID: "05e0", PID: "1200", Serial: "S1", Name: "Scanner", Hidraw: node}}, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opt) }()
	w, err := os.OpenFile(node, os.O_WRONLY, 0) // blocks until the agent opens it
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close(); cancel(); <-done })
	if _, err := w.Write([]byte{0x02, 0x00, 0x1e, 0x00}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the report recorded", func() bool { r, _ := srv.snapshot(); return len(r) > 0 })
	recs, devs := srv.snapshot()
	if string(recs[0].GetRaw()) != "\x02\x00\x1e\x00" || recs[0].GetPort() != node {
		t.Fatalf("record = %v", recs[0])
	}
	eventually(t, "the HID device listed", func() bool {
		_, devs = srv.snapshot()
		for _, d := range devs {
			if d.GetDeviceId() == "hid:05e0:1200:S1" && d.GetKind() == pb.DeviceKind_DEVICE_KIND_HID && d.GetProduct() == "Scanner" {
				return true
			}
		}
		return false
	})
}
