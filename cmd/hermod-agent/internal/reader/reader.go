// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package reader keeps one device open and turns what it sends into records:
// open it, frame its bytes, and when it goes away, open it again after a
// backoff. It knows nothing about where records go; Emit does.
package reader

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"time"

	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/framing"
	pb "github.com/gsoultan/hermod/pkg/agent/proto"
)

// Opener opens one connection to a device and says which port it used.
type Opener func(ctx context.Context) (io.ReadCloser, string, error)

// Emit receives one framed record.
type Emit func(raw []byte, port string, at time.Time)

// Status is what a reader is doing.
type Status int

// The statuses a reader reports.
const (
	StatusReading Status = iota + 1
	StatusDisconnected
)

func (s Status) String() string {
	switch s {
	case StatusReading:
		return "reading"
	case StatusDisconnected:
		return "disconnected"
	default:
		return "unknown"
	}
}

// Config is one device to read.
type Config struct {
	Open     Opener
	Framing  *pb.Framing
	Emit     Emit
	OnStatus func(Status, error)
	// Oversize, when set, counts records dropped for exceeding the framing's
	// size limit.
	Oversize *atomic.Uint64
	// Link, when set, lets commands be written to the device while it is
	// open, and receives every framed record as a possible reply.
	Link *Link
	// PerRead makes each read one record, without framing, for a source
	// that returns one message per read such as a hidraw node. The record
	// size limit still applies.
	PerRead bool
	// MinBackoff and MaxBackoff bound the wait between attempts to open the
	// device. Defaults 1 s and 30 s.
	MinBackoff, MaxBackoff time.Duration
}

func (c *Config) status(s Status, err error) {
	if c.OnStatus != nil {
		c.OnStatus(s, err)
	}
}

// Run reads the device until ctx ends.
func Run(ctx context.Context, cfg Config) {
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 30 * time.Second
	}
	wait := cfg.MinBackoff
	for ctx.Err() == nil {
		gotData, err := runOnce(ctx, &cfg)
		if ctx.Err() != nil {
			return
		}
		cfg.status(StatusDisconnected, err)
		if gotData {
			wait = cfg.MinBackoff
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, cfg.MaxBackoff)
	}
}

// runOnce opens the device and reads it until it fails. It reports whether
// any bytes arrived, so a device that works resets the backoff.
func runOnce(ctx context.Context, cfg *Config) (bool, error) {
	fr, err := framing.New(cfg.Framing)
	if err != nil {
		return false, err
	}
	rc, port, err := cfg.Open(ctx)
	if err != nil {
		return false, err
	}
	cfg.status(StatusReading, nil)
	return readConn(ctx, rc, port, fr, cfg)
}

type chunk struct {
	b   []byte
	err error
}

// readConn reads rc until it fails or ctx ends, closing it either way. Reads
// happen on their own goroutine so the idle-gap timer and cancellation work
// for any io.Reader, whatever its own timeout support.
func readConn(ctx context.Context, rc io.ReadCloser, port string, fr framing.Framer, cfg *Config) (bool, error) {
	defer func() { _ = rc.Close() }()
	if w, ok := rc.(io.Writer); ok && cfg.Link != nil {
		defer cfg.Link.attach(w)()
	}
	chunks := make(chan chunk)
	stop := make(chan struct{})
	defer close(stop)
	go pump(rc, chunks, stop)

	idle := time.Duration(cfg.Framing.GetIdleMs()) * time.Millisecond
	var timer <-chan time.Time
	gotData := false
	var seenOversize uint64
	for {
		select {
		case <-ctx.Done():
			return gotData, ctx.Err()
		case <-timer:
			timer = nil
			emitAll(cfg, fr.Idle(), port)
		case c := <-chunks:
			if c.err != nil {
				if errors.Is(c.err, io.EOF) {
					c.err = errors.New("device closed the connection")
				}
				return gotData, c.err
			}
			gotData = true
			if cfg.PerRead {
				if len(c.b) > recordLimit(cfg) {
					if cfg.Oversize != nil {
						cfg.Oversize.Add(1)
					}
					continue
				}
				emitAll(cfg, [][]byte{c.b}, port)
				continue
			}
			emitAll(cfg, fr.Feed(c.b), port)
			if idle > 0 {
				timer = time.After(idle)
			}
			if n := fr.Oversize(); n != seenOversize && cfg.Oversize != nil {
				cfg.Oversize.Add(n - seenOversize)
				seenOversize = n
			}
		}
	}
}

func pump(r io.Reader, out chan<- chunk, stop <-chan struct{}) {
	for {
		buf := make([]byte, 4096)
		n, err := r.Read(buf)
		if n > 0 {
			select {
			case out <- chunk{b: buf[:n]}:
			case <-stop:
				return
			}
		}
		if err != nil {
			select {
			case out <- chunk{err: err}:
			case <-stop:
			}
			return
		}
	}
}

// recordLimit is the framing's record size limit, or the default.
func recordLimit(cfg *Config) int {
	if n := int(cfg.Framing.GetMaxRecordBytes()); n > 0 {
		return n
	}
	return framing.DefaultMaxRecordBytes
}

func emitAll(cfg *Config, frames [][]byte, port string) {
	if len(frames) == 0 {
		return
	}
	now := time.Now()
	for _, f := range frames {
		cfg.Emit(f, port, now)
		if cfg.Link != nil {
			cfg.Link.deliver(f)
		}
	}
}
