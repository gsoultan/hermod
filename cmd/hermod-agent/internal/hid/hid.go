// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package hid finds USB HID devices, such as barcode scanners in keyboard
// mode, and reads them directly: in keyboard mode the device is grabbed, so
// a scan reaches the agent and is not typed into whatever window has focus.
//
// It works on Linux, through sysfs, evdev and hidraw. Elsewhere List finds
// nothing and opening a device fails; most scanners can be switched to
// USB-serial mode, which every platform reads as a serial device.
package hid

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Device is one HID interface.
type Device struct {
	ID       string // "hid:<vid>:<pid>:<serial>", or the physical path without a serial
	StableID bool   // false when the id falls back to the physical path
	VID, PID string
	Serial   string
	Name     string
	Hidraw   string // /dev/hidrawN, for raw reports
	Event    string // /dev/input/eventN, for key presses; empty when it has none
}

// List reads the HID devices from sysRoot (normally /sys) and names their
// nodes under devRoot (normally /dev).
func List(sysRoot, devRoot string) ([]Device, error) {
	dirs, err := filepath.Glob(filepath.Join(sysRoot, "class", "hidraw", "hidraw*"))
	if err != nil {
		return nil, err
	}
	sort.Strings(dirs)
	var out []Device
	for _, dir := range dirs {
		ue, err := readUevent(filepath.Join(dir, "device", "uevent"))
		if err != nil {
			continue
		}
		parts := strings.Split(ue["HID_ID"], ":")
		if len(parts) != 3 {
			continue
		}
		d := Device{
			VID: lastHex4(parts[1]), PID: lastHex4(parts[2]), Serial: ue["HID_UNIQ"], Name: ue["HID_NAME"],
			Hidraw: filepath.Join(devRoot, filepath.Base(dir)),
		}
		if ev, _ := filepath.Glob(filepath.Join(dir, "device", "input", "input*", "event*")); len(ev) > 0 {
			d.Event = filepath.Join(devRoot, "input", filepath.Base(ev[0]))
		}
		phys := ue["HID_PHYS"]
		if d.Serial != "" {
			d.ID, d.StableID = "hid:"+d.VID+":"+d.PID+":"+d.Serial, true
		} else {
			d.ID = "hid:" + d.VID + ":" + d.PID + ":" + phys
		}
		d.Name = strings.TrimSpace(d.Name)
		out = append(out, d)
	}
	// Interfaces of one device share its serial number: tell them apart by
	// the interface their physical path ends in.
	seen := map[string]int{}
	for _, d := range out {
		seen[d.ID]++
	}
	for i := range out {
		if seen[out[i].ID] > 1 {
			ue, _ := readUevent(filepath.Join(sysRoot, "class", "hidraw", filepath.Base(out[i].Hidraw), "device", "uevent"))
			out[i].ID += ":" + filepath.Base(ue["HID_PHYS"])
		}
	}
	return out, nil
}

func readUevent(path string) (map[string]string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: a sysfs path built from a glob
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := map[string]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		if k, v, ok := strings.Cut(s.Text(), "="); ok {
			out[k] = v
		}
	}
	return out, s.Err()
}

// lastHex4 turns sysfs's "000005E0" into "05e0".
func lastHex4(s string) string {
	s = strings.ToLower(s)
	if len(s) > 4 {
		s = s[len(s)-4:]
	}
	return s
}

// ErrUnsupported is opening a HID device where the agent cannot read one.
var ErrUnsupported = errors.New("USB HID devices are read on Linux only; switch the device to USB-serial mode if it has one")

// OpenKeyboard opens a device's key presses, grabbed so they reach only the
// agent, as text: one line per scan, ended by Enter.
func OpenKeyboard(event string) (io.ReadCloser, error) {
	if event == "" {
		return nil, errors.New("the HID device has no key input; read it in raw mode")
	}
	f, err := os.Open(event) //nolint:gosec // G304: an evdev node found under /dev/input
	if err != nil {
		return nil, err
	}
	if err := grab(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return NewKeyboard(f), nil
}

// OpenRaw opens a device's input reports: each read returns one report.
func OpenRaw(hidraw string) (io.ReadCloser, error) {
	if err := supported(); err != nil {
		return nil, err
	}
	return os.Open(hidraw) //nolint:gosec // G304: a hidraw node found under /dev
}

// Linux input event constants (linux/input-event-codes.h).
const (
	evKey         = 0x01
	keyEnter      = 28
	keyKPEnter    = 96
	keyLeftShift  = 42
	keyRightShift = 54
)

// timevalSize is the size of struct timeval at the head of an input_event:
// two longs.
const timevalSize = 2 * (32 << (^uint(0) >> 63)) / 8

const eventSize = timevalSize + 8

// keymap is the US layout: code -> {plain, shifted}.
var keymap = map[uint16][2]byte{
	2: {'1', '!'}, 3: {'2', '@'}, 4: {'3', '#'}, 5: {'4', '$'}, 6: {'5', '%'}, 7: {'6', '^'}, 8: {'7', '&'},
	9: {'8', '*'}, 10: {'9', '('}, 11: {'0', ')'}, 12: {'-', '_'}, 13: {'=', '+'}, 15: {'\t', '\t'},
	16: {'q', 'Q'}, 17: {'w', 'W'}, 18: {'e', 'E'}, 19: {'r', 'R'}, 20: {'t', 'T'}, 21: {'y', 'Y'}, 22: {'u', 'U'},
	23: {'i', 'I'}, 24: {'o', 'O'}, 25: {'p', 'P'}, 26: {'[', '{'}, 27: {']', '}'},
	30: {'a', 'A'}, 31: {'s', 'S'}, 32: {'d', 'D'}, 33: {'f', 'F'}, 34: {'g', 'G'}, 35: {'h', 'H'}, 36: {'j', 'J'},
	37: {'k', 'K'}, 38: {'l', 'L'}, 39: {';', ':'}, 40: {'\'', '"'}, 41: {'`', '~'}, 43: {'\\', '|'},
	44: {'z', 'Z'}, 45: {'x', 'X'}, 46: {'c', 'C'}, 47: {'v', 'V'}, 48: {'b', 'B'}, 49: {'n', 'N'}, 50: {'m', 'M'},
	51: {',', '<'}, 52: {'.', '>'}, 53: {'/', '?'}, 55: {'*', '*'}, 57: {' ', ' '},
	71: {'7', '7'}, 72: {'8', '8'}, 73: {'9', '9'}, 74: {'-', '-'}, 75: {'4', '4'}, 76: {'5', '5'}, 77: {'6', '6'},
	78: {'+', '+'}, 79: {'1', '1'}, 80: {'2', '2'}, 81: {'3', '3'}, 82: {'0', '0'}, 83: {'.', '.'}, 98: {'/', '/'},
}

// keyboard turns evdev key events into the text they type.
type keyboard struct {
	r     io.ReadCloser
	br    *bufio.Reader
	shift int
	out   []byte
}

// NewKeyboard reads input events from r and returns the text they type, US
// layout, with "\n" for Enter. Keys with no character are ignored.
func NewKeyboard(r io.ReadCloser) io.ReadCloser {
	return &keyboard{r: r, br: bufio.NewReaderSize(r, 64*eventSize)}
}

func (k *keyboard) Read(p []byte) (int, error) {
	ev := make([]byte, eventSize)
	for len(k.out) == 0 {
		if _, err := io.ReadFull(k.br, ev); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				err = io.EOF
			}
			return 0, err
		}
		typ := binary.NativeEndian.Uint16(ev[timevalSize:])
		code := binary.NativeEndian.Uint16(ev[timevalSize+2:])
		value := int32(binary.NativeEndian.Uint32(ev[timevalSize+4:]))
		if typ != evKey {
			continue
		}
		switch code {
		case keyLeftShift, keyRightShift:
			if value == 1 {
				k.shift++
			} else if value == 0 && k.shift > 0 {
				k.shift--
			}
			continue
		}
		if value != 1 { // only presses; not releases or auto-repeat
			continue
		}
		if code == keyEnter || code == keyKPEnter {
			k.out = append(k.out, '\n')
			continue
		}
		if c, ok := keymap[code]; ok {
			if k.shift > 0 {
				k.out = append(k.out, c[1])
			} else {
				k.out = append(k.out, c[0])
			}
		}
	}
	n := copy(p, k.out)
	k.out = k.out[n:]
	return n, nil
}

func (k *keyboard) Close() error { return k.r.Close() }
