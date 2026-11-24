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
	"fmt"
	"io"
	"sync"
	"time"
)

// ErrNotConnected is returned for a command to a device the reader does not
// have open. Nothing was written, so the command is safe to send again.
var ErrNotConnected = errors.New("device is not connected")

// WrittenError is a failure that came after the command's bytes went to the
// device, so sending the command again would repeat it.
type WrittenError struct{ Err error }

func (e *WrittenError) Error() string { return e.Err.Error() }
func (e *WrittenError) Unwrap() error { return e.Err }

// writeTimeout bounds one write, for connections that support a deadline.
const writeTimeout = 5 * time.Second

// Link lets commands reach a device while its reader has it open. One Link
// belongs to one device and outlives its reconnections.
type Link struct {
	// exchange serialises commands, so a reply always belongs to the
	// command that asked for it.
	exchange sync.Mutex

	mu      sync.Mutex
	conns   []io.Writer // open connections, newest last
	waiting chan []byte // set while a command waits for its reply
}

// NewLink returns a link with no device connected.
func NewLink() *Link { return &Link{} }

// Connected reports whether a command could be written now.
func (l *Link) Connected() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.conns) > 0
}

// attach records an open connection and returns the function that forgets
// it again.
func (l *Link) attach(w io.Writer) func() {
	l.mu.Lock()
	l.conns = append(l.conns, w)
	l.mu.Unlock()
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		for i, c := range l.conns {
			if c == w {
				l.conns = append(l.conns[:i], l.conns[i+1:]...)
				return
			}
		}
	}
}

// deliver hands a framed record to the command waiting for a reply, if any.
func (l *Link) deliver(raw []byte) {
	l.mu.Lock()
	ch := l.waiting
	l.waiting = nil
	l.mu.Unlock()
	if ch != nil {
		ch <- append([]byte(nil), raw...)
	}
}

// Send writes payload to the device, to its newest connection when it has
// several (a device in TCP listen mode). With a reply timeout above zero it
// then waits that long for the next record the device sends and returns it.
func (l *Link) Send(ctx context.Context, payload []byte, replyTimeout time.Duration) ([]byte, error) {
	l.exchange.Lock()
	defer l.exchange.Unlock()

	var reply chan []byte
	l.mu.Lock()
	if len(l.conns) == 0 {
		l.mu.Unlock()
		return nil, ErrNotConnected
	}
	w := l.conns[len(l.conns)-1]
	if replyTimeout > 0 {
		// Waiting before writing, so a device that answers at once is not
		// missed.
		reply = make(chan []byte, 1)
		l.waiting = reply
	}
	l.mu.Unlock()
	forget := func() {
		l.mu.Lock()
		if l.waiting == reply {
			l.waiting = nil
		}
		l.mu.Unlock()
	}

	if err := write(w, payload); err != nil {
		forget()
		// A partial write may have reached the device.
		return nil, &WrittenError{Err: fmt.Errorf("write: %w", err)}
	}
	if reply == nil {
		return nil, nil
	}
	timer := time.NewTimer(replyTimeout)
	defer timer.Stop()
	select {
	case r := <-reply:
		return r, nil
	case <-timer.C:
		forget()
		return nil, &WrittenError{Err: fmt.Errorf("no reply within %s", replyTimeout)}
	case <-ctx.Done():
		forget()
		return nil, &WrittenError{Err: ctx.Err()}
	}
}

func write(w io.Writer, b []byte) error {
	if d, ok := w.(interface{ SetWriteDeadline(time.Time) error }); ok {
		_ = d.SetWriteDeadline(time.Now().Add(writeTimeout))
		defer func() { _ = d.SetWriteDeadline(time.Time{}) }()
	}
	_, err := w.Write(b)
	return err
}
