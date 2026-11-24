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
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/gsoultan/hermod/pkg/agent/proto"
	"go.bug.st/serial"
)

type collected struct {
	mu     sync.Mutex
	frames []string
	ports  []string
	ch     chan struct{}
}

func newCollected() *collected { return &collected{ch: make(chan struct{}, 100)} }

func (c *collected) emit(raw []byte, port string, _ time.Time) {
	c.mu.Lock()
	c.frames = append(c.frames, string(raw))
	c.ports = append(c.ports, port)
	c.mu.Unlock()
	c.ch <- struct{}{}
}

func (c *collected) wait(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		c.mu.Lock()
		got := append([]string(nil), c.frames...)
		c.mu.Unlock()
		if len(got) >= n {
			return got
		}
		select {
		case <-c.ch:
		case <-deadline:
			t.Fatalf("got %d frames %q, want %d", len(got), got, n)
		}
	}
}

// pipeOpener hands out one end of a fresh in-memory connection per attempt
// and gives the test the other end to write to.
func pipeOpener(conns chan<- net.Conn) Opener {
	return func(context.Context) (io.ReadCloser, string, error) {
		a, b := net.Pipe()
		conns <- b
		return a, "pipe", nil
	}
}

func fastBackoff(cfg Config) Config {
	cfg.MinBackoff, cfg.MaxBackoff = time.Millisecond, 5*time.Millisecond
	return cfg
}

func TestRunFramesWhatTheDeviceSends(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	conns := make(chan net.Conn, 4)
	c := newCollected()
	go Run(ctx, fastBackoff(Config{Open: pipeOpener(conns), Emit: c.emit}))

	dev := <-conns
	_, _ = dev.Write([]byte("12.40 kg\r\n12.38"))
	_, _ = dev.Write([]byte(" kg\r\n"))
	got := c.wait(t, 2)
	if got[0] != "12.40 kg" || got[1] != "12.38 kg" || c.ports[0] != "pipe" {
		t.Fatalf("frames = %q ports = %q", got, c.ports)
	}
}

// A device that goes away (a cable pulled, a socket closed) is opened again,
// and a partial record from the old connection is not glued to the new one.
func TestRunReconnectsAndDropsAPartialRecord(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	conns := make(chan net.Conn, 4)
	c := newCollected()
	var statuses []Status
	var mu sync.Mutex
	cfg := fastBackoff(Config{Open: pipeOpener(conns), Emit: c.emit, OnStatus: func(s Status, _ error) {
		mu.Lock()
		statuses = append(statuses, s)
		mu.Unlock()
	}})
	go Run(ctx, cfg)

	first := <-conns
	_, _ = first.Write([]byte("one\npart"))
	c.wait(t, 1)
	_ = first.Close()

	second := <-conns
	_, _ = second.Write([]byte("two\n"))
	got := c.wait(t, 2)
	if got[1] != "two" {
		t.Fatalf("frames = %q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(statuses) < 3 || statuses[0] != StatusReading || statuses[1] != StatusDisconnected {
		t.Fatalf("statuses = %v", statuses)
	}
}

func TestRunRetriesWhenTheDeviceCannotBeOpened(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var attempts atomic.Int32
	conns := make(chan net.Conn, 4)
	inner := pipeOpener(conns)
	open := func(ctx context.Context) (io.ReadCloser, string, error) {
		if attempts.Add(1) < 3 {
			return nil, "", errors.New("no such port")
		}
		return inner(ctx)
	}
	c := newCollected()
	go Run(ctx, fastBackoff(Config{Open: open, Emit: c.emit}))
	dev := <-conns
	_, _ = dev.Write([]byte("hello\n"))
	c.wait(t, 1)
	if attempts.Load() < 3 {
		t.Fatalf("attempts = %d", attempts.Load())
	}
}

func TestRunIdleGapEmitsWhenTheLineGoesQuiet(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	conns := make(chan net.Conn, 1)
	c := newCollected()
	fr := &pb.Framing{Kind: pb.FramingKind_FRAMING_KIND_IDLE_GAP, IdleMs: 30}
	go Run(ctx, fastBackoff(Config{Open: pipeOpener(conns), Framing: fr, Emit: c.emit}))
	dev := <-conns
	_, _ = dev.Write([]byte("AB"))
	_, _ = dev.Write([]byte("CD"))
	if got := c.wait(t, 1); got[0] != "ABCD" {
		t.Fatalf("frames = %q", got)
	}
}

func TestRunStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	conns := make(chan net.Conn, 1)
	done := make(chan struct{})
	go func() {
		Run(ctx, fastBackoff(Config{Open: pipeOpener(conns), Emit: func([]byte, string, time.Time) {}}))
		close(done)
	}()
	<-conns
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRunCountsOversizeRecords(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	conns := make(chan net.Conn, 1)
	c := newCollected()
	var over atomic.Uint64
	go Run(ctx, fastBackoff(Config{Open: pipeOpener(conns), Framing: &pb.Framing{MaxRecordBytes: 3}, Emit: c.emit, Oversize: &over}))
	dev := <-conns
	_, _ = dev.Write([]byte("toolong\nok\n"))
	c.wait(t, 1)
	if over.Load() != 1 {
		t.Fatalf("oversize = %d", over.Load())
	}
}

func TestRunListenerFramesEachConnectionSeparately(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c := newCollected()
	go RunListener(ctx, ln, Config{Emit: c.emit})

	var d net.Dialer
	a, err := d.DialContext(t.Context(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := d.DialContext(t.Context(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	_, _ = a.Write([]byte("from-a-"))
	_, _ = b.Write([]byte("from-b\n"))
	_, _ = a.Write([]byte("done\n"))
	got := c.wait(t, 2)
	want := map[string]bool{"from-a-done": true, "from-b": true}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("frames = %q: connections were mixed", got)
		}
	}
}

func TestTCPClientOpenerConnects(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_, _ = conn.Write([]byte("hi\n"))
			_ = conn.Close()
		}
	}()
	rc, port, err := TCPClientOpener(ln.Addr().String())(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if port != ln.Addr().String() {
		t.Fatalf("port = %q", port)
	}
	b, _ := io.ReadAll(rc)
	if string(b) != "hi\n" {
		t.Fatalf("read %q", b)
	}
}

func TestSerialModeFromSettings(t *testing.T) {
	m := SerialMode(&pb.SerialSettings{BaudRate: 19200, DataBits: 7, Parity: pb.Parity_PARITY_EVEN, StopBits: pb.StopBits_STOP_BITS_TWO})
	if m.BaudRate != 19200 || m.DataBits != 7 || m.Parity != serial.EvenParity || m.StopBits != serial.TwoStopBits {
		t.Fatalf("mode = %+v", m)
	}
	d := SerialMode(&pb.SerialSettings{BaudRate: 9600})
	if d.DataBits != 8 || d.Parity != serial.NoParity || d.StopBits != serial.OneStopBit {
		t.Fatalf("defaults = %+v", d)
	}
}

func TestSerialOpenerFailsWhenTheDeviceIsGone(t *testing.T) {
	open := SerialOpener(func() (string, bool) { return "", false }, &pb.SerialSettings{BaudRate: 9600})
	if _, _, err := open(t.Context()); err == nil {
		t.Fatal("opened a device that is not attached")
	}
}

// With PerRead, each read is one record, as a hidraw node returns one report
// per read; framing does not apply.
func TestRunPerReadRecordsEachRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	conns := make(chan net.Conn, 1)
	c := newCollected()
	go Run(ctx, fastBackoff(Config{Open: pipeOpener(conns), Emit: c.emit, PerRead: true}))
	w := <-conns
	_, _ = w.Write([]byte{0x02, 0x00, 0x1e})
	_, _ = w.Write([]byte("\n\n"))
	got := c.wait(t, 2)
	if got[0] != "\x02\x00\x1e" || got[1] != "\n\n" {
		t.Fatalf("frames = %q", got)
	}
}
