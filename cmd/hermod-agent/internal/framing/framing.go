// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package framing cuts the byte stream a device sends into records. A serial
// port or a socket delivers bytes in whatever chunks it likes; a framer keeps
// what it has seen until a whole record is there.
package framing

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	pb "github.com/gsoultan/hermod/pkg/agent/proto"
)

// DefaultMaxRecordBytes is used when the settings leave the limit at zero.
const DefaultMaxRecordBytes = 64 << 10

// Framer turns chunks of bytes into records. It is not safe for concurrent
// use; each connection gets its own.
type Framer interface {
	// Feed takes the next chunk and returns the records it completed. The
	// returned slices are copies the caller may keep.
	Feed(p []byte) [][]byte
	// Idle is called when the line has been quiet for the idle gap. Only
	// idle-gap framing returns a record from it.
	Idle() [][]byte
	// Oversize counts records dropped for being longer than the limit.
	Oversize() uint64
}

// New builds the framer the settings describe. Nil settings mean one record
// per line.
func New(f *pb.Framing) (Framer, error) {
	if f == nil {
		f = &pb.Framing{}
	}
	limit := int(f.GetMaxRecordBytes())
	if limit == 0 {
		limit = DefaultMaxRecordBytes
	}
	build, ok := builders[f.GetKind()]
	if !ok {
		return nil, fmt.Errorf("framing: unknown framing kind %d", f.GetKind())
	}
	return build(f, base{max: limit})
}

var builders = map[pb.FramingKind]func(*pb.Framing, base) (Framer, error){
	pb.FramingKind_FRAMING_KIND_UNSPECIFIED:   newLine,
	pb.FramingKind_FRAMING_KIND_LINE:          newLine,
	pb.FramingKind_FRAMING_KIND_DELIMITER:     newDelimiter,
	pb.FramingKind_FRAMING_KIND_FIXED_LENGTH:  newFixed,
	pb.FramingKind_FRAMING_KIND_START_END:     newStartEnd,
	pb.FramingKind_FRAMING_KIND_LENGTH_PREFIX: newPrefix,
	pb.FramingKind_FRAMING_KIND_IDLE_GAP:      newIdle,
}

func newLine(_ *pb.Framing, b base) (Framer, error) { return &lineFramer{base: b}, nil }

func newDelimiter(f *pb.Framing, b base) (Framer, error) {
	if len(f.GetDelimiter()) == 0 {
		return nil, errors.New("framing: delimiter framing needs delimiter bytes")
	}
	return &delimFramer{base: b, delim: bytes.Clone(f.GetDelimiter())}, nil
}

func newFixed(f *pb.Framing, b base) (Framer, error) {
	if f.GetLength() == 0 || int(f.GetLength()) > b.max {
		return nil, fmt.Errorf("framing: fixed length must be 1 to %d bytes", b.max)
	}
	return &fixedFramer{base: b, n: int(f.GetLength())}, nil
}

func newStartEnd(f *pb.Framing, b base) (Framer, error) {
	if len(f.GetStartBytes()) == 0 || len(f.GetEndBytes()) == 0 {
		return nil, errors.New("framing: start-end framing needs start and end bytes")
	}
	return &startEndFramer{base: b, start: bytes.Clone(f.GetStartBytes()), end: bytes.Clone(f.GetEndBytes())}, nil
}

func newPrefix(f *pb.Framing, b base) (Framer, error) {
	ps := int(f.GetPrefixSize())
	if ps != 1 && ps != 2 && ps != 4 {
		return nil, errors.New("framing: length prefix must be 1, 2 or 4 bytes")
	}
	return &prefixFramer{base: b, size: ps}, nil
}

func newIdle(f *pb.Framing, b base) (Framer, error) {
	if f.GetIdleMs() == 0 {
		return nil, errors.New("framing: idle-gap framing needs an idle time")
	}
	return &idleFramer{base: b}, nil
}

// base holds what every framer shares: the pending bytes, the size limit and
// the count of records dropped for exceeding it.
type base struct {
	buf      []byte
	max      int
	oversize uint64
}

func (b *base) Oversize() uint64 { return b.oversize }
func (b *base) Idle() [][]byte   { return nil }

// take returns a copy of rec when it is within the limit, counting it
// otherwise. Empty records are dropped silently: they are line noise, not
// readings.
func (b *base) take(out [][]byte, rec []byte) [][]byte {
	switch {
	case len(rec) == 0:
		return out
	case len(rec) > b.max:
		b.oversize++
		return out
	default:
		return append(out, bytes.Clone(rec))
	}
}

// consume drops the first n pending bytes without letting the buffer's
// backing array grow for ever.
func (b *base) consume(n int) {
	b.buf = append(b.buf[:0], b.buf[n:]...)
}

type lineFramer struct {
	base
	afterCR    bool
	discarding bool
}

func (l *lineFramer) Feed(p []byte) [][]byte {
	var out [][]byte
	for _, c := range p {
		switch {
		case c == '\n' && l.afterCR:
			l.afterCR = false
		case c == '\n' || c == '\r':
			l.afterCR = c == '\r'
			if !l.discarding {
				out = l.take(out, l.buf)
			}
			l.discarding = false
			l.buf = l.buf[:0]
		default:
			l.afterCR = false
			if l.discarding {
				continue
			}
			l.buf = append(l.buf, c)
			if len(l.buf) > l.max {
				l.oversize++
				l.discarding = true
				l.buf = l.buf[:0]
			}
		}
	}
	return out
}

type delimFramer struct {
	base
	delim      []byte
	discarding bool
}

func (d *delimFramer) Feed(p []byte) [][]byte {
	d.buf = append(d.buf, p...)
	var out [][]byte
	for {
		i := bytes.Index(d.buf, d.delim)
		if i < 0 {
			d.trimUnterminated()
			return out
		}
		if !d.discarding {
			out = d.take(out, d.buf[:i])
		}
		d.discarding = false
		d.consume(i + len(d.delim))
	}
}

// trimUnterminated stops a record with no delimiter in sight from growing
// past the limit. It keeps the bytes that could be the start of a delimiter
// split across chunks.
func (d *delimFramer) trimUnterminated() {
	if len(d.buf) <= d.max+len(d.delim) {
		return
	}
	if !d.discarding {
		d.oversize++
		d.discarding = true
	}
	d.consume(len(d.buf) - (len(d.delim) - 1))
}

type fixedFramer struct {
	base
	n int
}

func (f *fixedFramer) Feed(p []byte) [][]byte {
	f.buf = append(f.buf, p...)
	var out [][]byte
	for len(f.buf) >= f.n {
		out = f.take(out, f.buf[:f.n])
		f.consume(f.n)
	}
	return out
}

type startEndFramer struct {
	base
	start, end []byte
	inside     bool
}

func (s *startEndFramer) Feed(p []byte) [][]byte {
	s.buf = append(s.buf, p...)
	var out [][]byte
	for {
		if !s.inside {
			i := bytes.Index(s.buf, s.start)
			if i < 0 {
				s.consume(max(0, len(s.buf)-(len(s.start)-1)))
				return out
			}
			s.consume(i + len(s.start))
			s.inside = true
		}
		e := bytes.Index(s.buf, s.end)
		// Another start before the end means the record in progress was
		// noise; begin again from the new start.
		if r := bytes.Index(s.buf, s.start); r >= 0 && (e < 0 || r < e) {
			s.consume(r + len(s.start))
			continue
		}
		if e < 0 {
			if len(s.buf) > s.max+len(s.end) {
				s.oversize++
				s.inside = false
				s.buf = s.buf[:0]
			}
			return out
		}
		out = s.take(out, s.buf[:e])
		s.consume(e + len(s.end))
		s.inside = false
	}
}

type prefixFramer struct {
	base
	size int
	need int  // payload length of the record being read
	have bool // the length prefix of the current record has been read
	skip int  // bytes still to throw away from an oversize record
}

func (l *prefixFramer) Feed(p []byte) [][]byte {
	l.buf = append(l.buf, p...)
	var out [][]byte
	for {
		if l.skip > 0 {
			n := min(l.skip, len(l.buf))
			l.consume(n)
			l.skip -= n
			if l.skip > 0 {
				return out
			}
		}
		if !l.have {
			if len(l.buf) < l.size {
				return out
			}
			n := l.readPrefix()
			l.consume(l.size)
			if n > l.max {
				l.oversize++
				l.skip = n
				continue
			}
			l.need, l.have = n, true
		}
		if len(l.buf) < l.need {
			return out
		}
		out = l.take(out, l.buf[:l.need])
		l.consume(l.need)
		l.have = false
	}
}

func (l *prefixFramer) readPrefix() int {
	switch l.size {
	case 1:
		return int(l.buf[0])
	case 2:
		return int(binary.BigEndian.Uint16(l.buf))
	default:
		return int(binary.BigEndian.Uint32(l.buf))
	}
}

type idleFramer struct {
	base
	discarding bool
}

func (i *idleFramer) Feed(p []byte) [][]byte {
	if i.discarding {
		return nil
	}
	i.buf = append(i.buf, p...)
	if len(i.buf) > i.max {
		i.oversize++
		i.discarding = true
		i.buf = i.buf[:0]
	}
	return nil
}

func (i *idleFramer) Idle() [][]byte {
	out := i.take(nil, i.buf)
	i.buf = i.buf[:0]
	i.discarding = false
	return out
}
