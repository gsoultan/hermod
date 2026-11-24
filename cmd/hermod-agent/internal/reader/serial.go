// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package reader

import (
	"context"
	"errors"
	"io"

	pb "github.com/gsoultan/hermod/pkg/agent/proto"
	"go.bug.st/serial"
)

// SerialMode turns settings into the serial library's mode, filling in the
// usual 8N1 for what the settings leave unset.
func SerialMode(s *pb.SerialSettings) *serial.Mode {
	m := &serial.Mode{BaudRate: int(s.GetBaudRate()), DataBits: int(s.GetDataBits())}
	if m.DataBits == 0 {
		m.DataBits = 8
	}
	switch s.GetParity() {
	case pb.Parity_PARITY_ODD:
		m.Parity = serial.OddParity
	case pb.Parity_PARITY_EVEN:
		m.Parity = serial.EvenParity
	case pb.Parity_PARITY_MARK:
		m.Parity = serial.MarkParity
	case pb.Parity_PARITY_SPACE:
		m.Parity = serial.SpaceParity
	default:
		m.Parity = serial.NoParity
	}
	switch s.GetStopBits() {
	case pb.StopBits_STOP_BITS_ONE_POINT_FIVE:
		m.StopBits = serial.OnePointFiveStopBits
	case pb.StopBits_STOP_BITS_TWO:
		m.StopBits = serial.TwoStopBits
	default:
		m.StopBits = serial.OneStopBit
	}
	return m
}

// SerialOpener opens a serial device. resolve gives the port the device is
// on right now, because the operating system may give it a different one
// after a replug; it reports false while the device is not attached.
func SerialOpener(resolve func() (string, bool), s *pb.SerialSettings) Opener {
	return func(context.Context) (io.ReadCloser, string, error) {
		port, ok := resolve()
		if !ok {
			return nil, "", errors.New("device is not attached")
		}
		p, err := serial.Open(port, SerialMode(s))
		if err != nil {
			return nil, "", err
		}
		return p, port, nil
	}
}
