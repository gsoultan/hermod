// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package spool

import (
	"bufio"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"math"
)

// A record on disk is a frame: a 4-byte payload length, a 4-byte CRC-32C of
// the payload, then the payload. The CRC is what lets Open tell a torn tail —
// a crash in the middle of a write — from a whole record.
const frameHeader = 8

// maxPayload bounds what a length prefix may claim, so a corrupt length cannot
// make Open allocate gigabytes before the CRC check rejects the frame.
const maxPayload = 16 << 20

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

var errBadFrame = errors.New("spool: bad frame")

func appendFrame(dst []byte, r *Record) ([]byte, error) {
	if len(r.DeviceID) > math.MaxUint16 || len(r.Port) > math.MaxUint16 {
		return dst, errors.New("spool: device id or port longer than 65535 bytes")
	}
	size := 8 + 8 + 2 + len(r.DeviceID) + 2 + len(r.Port) + 4 + len(r.Raw)
	if size > maxPayload {
		return dst, errors.New("spool: record larger than 16 MiB")
	}
	p := make([]byte, 0, size)
	p = binary.BigEndian.AppendUint64(p, r.Seq)
	p = binary.BigEndian.AppendUint64(p, uint64(r.ReceivedAt))
	p = binary.BigEndian.AppendUint16(p, uint16(len(r.DeviceID)))
	p = append(p, r.DeviceID...)
	p = binary.BigEndian.AppendUint16(p, uint16(len(r.Port)))
	p = append(p, r.Port...)
	p = binary.BigEndian.AppendUint32(p, uint32(len(r.Raw)))
	p = append(p, r.Raw...)

	dst = binary.BigEndian.AppendUint32(dst, uint32(len(p)))
	dst = binary.BigEndian.AppendUint32(dst, crc32.Checksum(p, castagnoli))
	return append(dst, p...), nil
}

// readFrame reads one record. It returns io.EOF at a clean end and
// errBadFrame for a partial or corrupt frame, along with the bytes consumed
// so callers can track offsets.
func readFrame(r *bufio.Reader) (Record, int64, error) {
	var hdr [frameHeader]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return Record{}, 0, io.EOF
		}
		return Record{}, 0, errBadFrame
	}
	n := binary.BigEndian.Uint32(hdr[:4])
	if n > maxPayload {
		return Record{}, 0, errBadFrame
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return Record{}, 0, errBadFrame
	}
	if crc32.Checksum(p, castagnoli) != binary.BigEndian.Uint32(hdr[4:]) {
		return Record{}, 0, errBadFrame
	}
	rec, err := decodePayload(p)
	return rec, frameHeader + int64(n), err
}

func decodePayload(p []byte) (Record, error) {
	var rec Record
	take := func(n int) ([]byte, bool) {
		if len(p) < n {
			return nil, false
		}
		b := p[:n]
		p = p[n:]
		return b, true
	}
	b, ok := take(16)
	if !ok {
		return rec, errBadFrame
	}
	rec.Seq = binary.BigEndian.Uint64(b[:8])
	rec.ReceivedAt = int64(binary.BigEndian.Uint64(b[8:]))
	for _, field := range []*string{&rec.DeviceID, &rec.Port} {
		l, ok := take(2)
		if !ok {
			return rec, errBadFrame
		}
		s, ok := take(int(binary.BigEndian.Uint16(l)))
		if !ok {
			return rec, errBadFrame
		}
		*field = string(s)
	}
	l, ok := take(4)
	if !ok {
		return rec, errBadFrame
	}
	raw, ok := take(int(binary.BigEndian.Uint32(l)))
	if !ok || len(p) != 0 {
		return rec, errBadFrame
	}
	rec.Raw = append([]byte(nil), raw...)
	return rec, nil
}
