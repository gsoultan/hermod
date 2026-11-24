// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package modbus

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Write is one write a command asks for.
type Write struct {
	Table     Table // Coils or HoldingRegisters
	Address   uint16
	Registers []uint16
	Bits      []bool
}

// ParseWrite reads a command's text: "holding <address> <value>..." with
// values 0 to 65535, or "coil <address> <value>..." with values 1, 0, on or
// off. Values are written from address on. Nothing else is accepted.
func ParseWrite(payload []byte) (Write, error) {
	f := strings.Fields(string(payload))
	if len(f) < 3 {
		return Write{}, errors.New(`a Modbus command is "holding <address> <value>..." or "coil <address> <value>..."`)
	}
	addr, err := strconv.ParseUint(f[1], 10, 16)
	if err != nil {
		return Write{}, fmt.Errorf("address %q: must be 0 to 65535", f[1])
	}
	values := f[2:]
	if int(addr)+len(values) > 65536 {
		return Write{}, errors.New("the write runs past address 65535")
	}
	w := Write{Address: uint16(addr)}
	switch f[0] {
	case "holding":
		if len(values) > MaxWriteRegisters {
			return Write{}, fmt.Errorf("at most %d registers in one write", MaxWriteRegisters)
		}
		w.Table = HoldingRegisters
		for _, v := range values {
			n, err := strconv.ParseUint(v, 10, 16)
			if err != nil {
				return Write{}, fmt.Errorf("value %q: must be 0 to 65535", v)
			}
			w.Registers = append(w.Registers, uint16(n))
		}
	case "coil":
		if len(values) > MaxWriteBits {
			return Write{}, fmt.Errorf("at most %d coils in one write", MaxWriteBits)
		}
		w.Table = Coils
		for _, v := range values {
			switch strings.ToLower(v) {
			case "1", "on", "true":
				w.Bits = append(w.Bits, true)
			case "0", "off", "false":
				w.Bits = append(w.Bits, false)
			default:
				return Write{}, fmt.Errorf("coil value %q: must be 1, 0, on or off", v)
			}
		}
	default:
		return Write{}, fmt.Errorf("unknown Modbus write %q: holding or coil", f[0])
	}
	return w, nil
}

// Do performs w.
func (c *Client) Do(ctx context.Context, w Write) error {
	if w.Table == Coils {
		return c.WriteCoils(ctx, w.Address, w.Bits)
	}
	return c.WriteRegisters(ctx, w.Address, w.Registers)
}
