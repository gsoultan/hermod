// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package spool is hermod-agent's disk buffer. A device cannot resend a
// reading the agent missed, so every record is written here, synced, and kept
// until the Hermod server acknowledges it. Records carry a sequence number
// that increases by one per record and never repeats for one spool directory.
package spool

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

// Record is one framed reading from a device.
type Record struct {
	Seq        uint64
	DeviceID   string
	Port       string
	ReceivedAt int64 // nanoseconds since the Unix epoch, by the agent's clock
	Raw        []byte
}

// Options size the spool. Zero values take the defaults.
type Options struct {
	// MaxBytes caps the spool on disk. When an append takes it over, the
	// oldest segments are deleted and their unacknowledged records counted
	// as dropped. Default 1 GiB.
	MaxBytes int64
	// SegmentBytes is the size at which a new segment file is started.
	// Default 16 MiB.
	SegmentBytes int64
}

// Stats describes what the spool holds.
type Stats struct {
	LastSeq        uint64
	AckedSeq       uint64
	Waiting        uint64
	Bytes          int64
	OldestUnixNano int64 // received time of the oldest waiting record; 0 when none
	Dropped        uint64
}

// Spool is safe for concurrent use.
type Spool struct {
	mu      sync.Mutex
	dir     string
	opt     Options
	segs    []*segment
	active  *os.File
	lastSeq uint64
	st      state
	notify  chan struct{}
	cursor  readCursor
	closed  bool
}

// readCursor remembers where the last sequential read stopped, so a reader
// walking forward does not rescan a segment from its start on every call.
type readCursor struct {
	seg *segment
	off int64
	seq uint64
}

// Open opens or creates the spool in dir.
func Open(dir string, opt Options) (*Spool, error) {
	if opt.MaxBytes <= 0 {
		opt.MaxBytes = 1 << 30
	}
	if opt.SegmentBytes <= 0 {
		opt.SegmentBytes = 16 << 20
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	st, err := loadState(dir)
	if err != nil {
		return nil, fmt.Errorf("spool: read state: %w", err)
	}
	segs, err := listSegments(dir)
	if err != nil {
		return nil, err
	}
	s := &Spool{dir: dir, opt: opt, st: st, notify: make(chan struct{}, 1)}
	if err := s.recover(segs); err != nil {
		return nil, err
	}
	return s, nil
}

// recover scans the segments found on disk, dropping a torn tail and empty
// leftovers, and works out the last sequence number.
func (s *Spool) recover(segs []*segment) error {
	for i, seg := range segs {
		if err := seg.scan(); err != nil {
			return fmt.Errorf("spool: scan %s: %w", seg.path, err)
		}
		if seg.count == 0 && i < len(segs)-1 {
			if err := os.Remove(seg.path); err != nil {
				return err
			}
			continue
		}
		s.segs = append(s.segs, seg)
		if seg.count > 0 {
			s.lastSeq = seg.last
		}
	}
	// Sequence numbers never repeat, even when every segment has been
	// acknowledged and deleted.
	s.lastSeq = max(s.lastSeq, s.st.Acked)
	if len(s.segs) > 0 && s.segs[0].count > 0 && s.st.Acked+1 < s.segs[0].first {
		s.st.Acked = s.segs[0].first - 1
	}
	// A crash between saving an acknowledgement and deleting the segments it
	// covered leaves them behind; finish the job.
	for len(s.segs) > 1 && s.segs[0].last <= s.st.Acked {
		if err := s.removeOldest(); err != nil {
			return err
		}
	}
	return nil
}

// Notify returns a channel that receives after records are appended.
func (s *Spool) Notify() <-chan struct{} { return s.notify }

// Append writes recs durably, assigning their sequence numbers, and returns
// them with Seq set. Nothing is returned until the write is synced.
func (s *Spool) Append(recs []Record) ([]Record, error) {
	if len(recs) == 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("spool: closed")
	}
	if err := s.ensureActive(); err != nil {
		return nil, err
	}
	out := make([]Record, len(recs))
	var buf []byte
	for i := range recs {
		out[i] = recs[i]
		out[i].Seq = s.lastSeq + uint64(i) + 1
		var err error
		if buf, err = appendFrame(buf, &out[i]); err != nil {
			return nil, err
		}
	}
	if err := s.write(buf); err != nil {
		return nil, err
	}
	seg := s.segs[len(s.segs)-1]
	seg.count += uint64(len(out))
	seg.last = out[len(out)-1].Seq
	seg.size += int64(len(buf))
	s.lastSeq = seg.last
	if err := s.enforceCap(); err != nil {
		return out, err
	}
	select {
	case s.notify <- struct{}{}:
	default:
	}
	return out, nil
}

func (s *Spool) write(buf []byte) error {
	if _, err := s.active.Write(buf); err != nil {
		return fmt.Errorf("spool: write: %w", err)
	}
	if err := s.active.Sync(); err != nil {
		return fmt.Errorf("spool: sync: %w", err)
	}
	return nil
}

// ensureActive opens the segment to append to, starting a new one when there
// is none or the current one is full.
func (s *Spool) ensureActive() error {
	if n := len(s.segs); n > 0 && s.segs[n-1].size < s.opt.SegmentBytes {
		if s.active != nil {
			return nil
		}
		f, err := os.OpenFile(s.segs[n-1].path, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		s.active = f
		return nil
	}
	if s.active != nil {
		if err := s.active.Close(); err != nil {
			return err
		}
		s.active = nil
	}
	seg := &segment{path: segmentPath(s.dir, s.lastSeq+1), first: s.lastSeq + 1}
	f, err := os.OpenFile(seg.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if err := syncDir(s.dir); err != nil {
		_ = f.Close()
		return err
	}
	s.active = f
	s.segs = append(s.segs, seg)
	return nil
}

// enforceCap deletes the oldest segments while the spool is over MaxBytes,
// counting any unacknowledged records in them as dropped. The active segment
// is never deleted.
func (s *Spool) enforceCap() error {
	changed := false
	for len(s.segs) > 1 && s.totalBytes() > s.opt.MaxBytes {
		seg := s.segs[0]
		if seg.count > 0 && seg.last > s.st.Acked {
			s.st.Dropped += seg.last - max(s.st.Acked, seg.first-1)
			s.st.Acked = seg.last
		}
		if err := s.removeOldest(); err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return nil
	}
	return saveState(s.dir, s.st)
}

func (s *Spool) removeOldest() error {
	seg := s.segs[0]
	if err := os.Remove(seg.path); err != nil {
		return err
	}
	if s.cursor.seg == seg {
		s.cursor = readCursor{}
	}
	s.segs = s.segs[1:]
	return nil
}

func (s *Spool) totalBytes() int64 {
	var n int64
	for _, seg := range s.segs {
		n += seg.size
	}
	return n
}

// Ack records that every record up to and including upTo was delivered, and
// deletes the segments that hold only delivered records.
func (s *Spool) Ack(upTo uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if upTo > s.lastSeq {
		return fmt.Errorf("spool: ack %d beyond last sequence %d", upTo, s.lastSeq)
	}
	if upTo <= s.st.Acked {
		return nil
	}
	s.st.Acked = upTo
	if err := saveState(s.dir, s.st); err != nil {
		return err
	}
	for len(s.segs) > 1 && s.segs[0].last <= s.st.Acked {
		if err := s.removeOldest(); err != nil {
			return err
		}
	}
	return nil
}

// ReadFrom returns up to limit records with a sequence number above after,
// in order.
func (s *Spool) ReadFrom(after uint64, limit int) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(after, limit, true)
}

func (s *Spool) read(after uint64, limit int, useCursor bool) ([]Record, error) {
	var out []Record
	for i, seg := range s.segs {
		if seg.count == 0 || seg.last <= after || len(out) >= limit {
			continue
		}
		from := int64(0)
		if useCursor && s.cursor.seg == seg && s.cursor.seq == after && len(out) == 0 {
			from = s.cursor.off
		}
		recs, off, err := seg.readAfter(after, from, limit-len(out))
		if err != nil {
			return out, err
		}
		out = append(out, recs...)
		if useCursor && len(recs) > 0 {
			s.cursor = readCursor{seg: s.segs[i], off: off, seq: recs[len(recs)-1].Seq}
		}
	}
	return out, nil
}

// Stats reports what the spool holds.
func (s *Spool) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Stats{
		LastSeq:  s.lastSeq,
		AckedSeq: s.st.Acked,
		Waiting:  s.lastSeq - s.st.Acked,
		Bytes:    s.totalBytes(),
		Dropped:  s.st.Dropped,
	}
	if st.Waiting > 0 {
		if recs, err := s.read(s.st.Acked, 1, false); err == nil && len(recs) == 1 {
			st.OldestUnixNano = recs[0].ReceivedAt
		}
	}
	return st
}

// Close closes the active segment. The spool cannot be used after.
func (s *Spool) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.active == nil {
		return nil
	}
	err := s.active.Close()
	s.active = nil
	return err
}
