// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package hid

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// fakeSys builds the sysfs entries of a hidraw device with an event node.
func fakeSys(t *testing.T, root, hidraw, uevent, event string) {
	t.Helper()
	dev := filepath.Join(root, "class", "hidraw", hidraw, "device")
	if err := os.MkdirAll(filepath.Join(dev, "input", "input9", event), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dev, "uevent"), []byte(uevent), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListReadsSysfs(t *testing.T) {
	root := t.TempDir()
	fakeSys(t, root, "hidraw0", "DRIVER=hid-generic\nHID_ID=0003:000005E0:00001200\nHID_NAME=Symbol Bar Code Scanner\nHID_PHYS=usb-0000:00:14.0-1/input0\nHID_UNIQ=S123\n", "event5")
	fakeSys(t, root, "hidraw1", "HID_ID=0003:0000046D:0000C077\nHID_NAME=Logitech Mouse\nHID_PHYS=usb-0000:00:14.0-2/input0\nHID_UNIQ=\n", "event6")
	got, err := List(root, "/dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	s := got[0]
	if s.ID != "hid:05e0:1200:S123" || !s.StableID || s.VID != "05e0" || s.PID != "1200" || s.Name != "Symbol Bar Code Scanner" ||
		s.Hidraw != "/dev/hidraw0" || s.Event != "/dev/input/event5" {
		t.Fatalf("scanner = %+v", s)
	}
	m := got[1]
	if m.ID != "hid:046d:c077:usb-0000:00:14.0-2/input0" || m.StableID {
		t.Fatalf("mouse = %+v", m)
	}
}

// Two interfaces of one scanner share a serial number; each keeps its own id.
func TestListSeparatesInterfacesOfOneDevice(t *testing.T) {
	root := t.TempDir()
	fakeSys(t, root, "hidraw0", "HID_ID=0003:000005E0:00001200\nHID_PHYS=usb-1/input0\nHID_UNIQ=S1\n", "event5")
	fakeSys(t, root, "hidraw1", "HID_ID=0003:000005E0:00001200\nHID_PHYS=usb-1/input1\nHID_UNIQ=S1\n", "event6")
	got, _ := List(root, "/dev")
	if len(got) != 2 || got[0].ID != "hid:05e0:1200:S1:input0" || got[1].ID != "hid:05e0:1200:S1:input1" {
		t.Fatalf("got %+v", got)
	}
}

func TestListWithoutHidrawIsEmpty(t *testing.T) {
	got, err := List(t.TempDir(), "/dev")
	if err != nil || len(got) != 0 {
		t.Fatalf("List = %v, %v", got, err)
	}
}

// keys writes key events as the kernel does: press then release.
func keys(codes ...uint16) []byte {
	var b bytes.Buffer
	ev := func(typ, code uint16, value int32) {
		b.Write(make([]byte, timevalSize))
		_ = binary.Write(&b, binary.NativeEndian, typ)
		_ = binary.Write(&b, binary.NativeEndian, code)
		_ = binary.Write(&b, binary.NativeEndian, value)
	}
	for _, c := range codes {
		if c == keyLeftShift {
			ev(evKey, c, 1)
			continue
		}
		ev(evKey, c, 1)
		ev(0, 0, 0) // a sync event, ignored
		ev(evKey, c, 0)
	}
	return b.Bytes()
}

func TestKeyboardTurnsKeyPressesIntoText(t *testing.T) {
	// "Ab-1" Enter, as a scanner types it: shift+a, b, minus, 1, enter.
	in := keys(keyLeftShift, 30)
	in = append(in, keyRelease(keyLeftShift)...)
	in = append(in, keys(48, 12, 2, 28)...)
	r := NewKeyboard(io.NopCloser(bytes.NewReader(in)))
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "Ab-1\n" {
		t.Fatalf("text = %q", out)
	}
}

func keyRelease(code uint16) []byte {
	var b bytes.Buffer
	b.Write(make([]byte, timevalSize))
	_ = binary.Write(&b, binary.NativeEndian, uint16(evKey))
	_ = binary.Write(&b, binary.NativeEndian, code)
	_ = binary.Write(&b, binary.NativeEndian, int32(0))
	return b.Bytes()
}
