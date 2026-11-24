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
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// startLinked runs a reader whose device is the far end of a pipe, and
// returns that end once the reader has it open.
func startLinked(t *testing.T, link *Link) (net.Conn, *collected) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	conns := make(chan net.Conn, 4)
	c := newCollected()
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, fastBackoff(Config{Open: pipeOpener(conns), Emit: c.emit, Link: link}))
	}()
	t.Cleanup(func() { cancel(); <-done })
	dev := <-conns
	waitConnected(t, link)
	return dev, c
}

func waitConnected(t *testing.T, link *Link) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !link.Connected() {
		if time.Now().After(deadline) {
			t.Fatal("link never connected")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLinkWritesTheCommandToTheDevice(t *testing.T) {
	link := NewLink()
	dev, _ := startLinked(t, link)
	got := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(dev).ReadString('\n')
		got <- line
	}()
	reply, err := link.Send(t.Context(), []byte("T\r\n"), 0)
	if err != nil || reply != nil {
		t.Fatalf("reply %q err %v", reply, err)
	}
	if line := <-got; line != "T\r\n" {
		t.Fatalf("device got %q", line)
	}
}

func TestLinkReturnsTheNextRecordAsTheReply(t *testing.T) {
	link := NewLink()
	dev, c := startLinked(t, link)
	go func() {
		r := bufio.NewReader(dev)
		if line, _ := r.ReadString('\n'); line == "W\n" {
			_, _ = dev.Write([]byte("12.40 kg\r\n"))
		}
	}()
	reply, err := link.Send(t.Context(), []byte("W\n"), time.Second)
	if err != nil || string(reply) != "12.40 kg" {
		t.Fatalf("reply %q err %v", reply, err)
	}
	// The reply is a reading like any other and is still emitted.
	if got := c.wait(t, 1); got[0] != "12.40 kg" {
		t.Fatalf("emitted %q", got)
	}
}

func TestLinkFailsWhenNoReplyComesInTime(t *testing.T) {
	link := NewLink()
	dev, _ := startLinked(t, link)
	go func() { _, _ = bufio.NewReader(dev).ReadString('\n') }()
	_, err := link.Send(t.Context(), []byte("W\n"), 30*time.Millisecond)
	var we *WrittenError
	if !errors.As(err, &we) || !strings.Contains(err.Error(), "no reply") {
		t.Fatalf("err = %v, want a no-reply error marked written", err)
	}
}

func TestLinkRefusesWhenTheDeviceIsNotOpen(t *testing.T) {
	link := NewLink()
	_, err := link.Send(t.Context(), []byte("T"), 0)
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v", err)
	}
	var we *WrittenError
	if errors.As(err, &we) {
		t.Fatal("a command that never reached the device is reported as written")
	}
}

// Two commands that both expect a reply must not take each other's: the
// second is not written until the first has its reply.
func TestLinkKeepsEachReplyWithItsCommand(t *testing.T) {
	link := NewLink()
	dev, _ := startLinked(t, link)
	go func() {
		r := bufio.NewReader(dev)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			_, _ = dev.Write([]byte("re:" + line))
		}
	}()
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := range 10 {
		wg.Go(func() {
			cmd := string(rune('a'+i)) + "\n"
			reply, err := link.Send(t.Context(), []byte(cmd), time.Second)
			if err != nil {
				errs <- err
				return
			}
			if string(reply) != "re:"+strings.TrimSuffix(cmd, "\n") {
				errs <- errors.New("command " + cmd + " got reply " + string(reply))
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestLinkDisconnectsWithTheDevice(t *testing.T) {
	link := NewLink()
	ctx, cancel := context.WithCancel(t.Context())
	conns := make(chan net.Conn, 1)
	opened := false
	open := func(ctx context.Context) (io.ReadCloser, string, error) {
		if opened {
			return nil, "", errors.New("device is gone")
		}
		opened = true
		return pipeOpener(conns)(ctx)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, fastBackoff(Config{Open: open, Emit: newCollected().emit, Link: link}))
	}()
	t.Cleanup(func() { cancel(); <-done })
	dev := <-conns
	waitConnected(t, link)
	_ = dev.Close()
	deadline := time.Now().Add(5 * time.Second)
	for link.Connected() {
		if time.Now().After(deadline) {
			t.Fatal("link still connected after the device closed")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := link.Send(t.Context(), []byte("T"), 0); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("send after disconnect: %v", err)
	}
}
