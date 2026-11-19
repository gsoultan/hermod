// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package rediskv

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"syscall"

	"github.com/redis/go-redis/v9"
)

// greeting keeps the reason Redis gave for refusing a connection.
//
// Redis refuses some clients as soon as they connect: in protected-mode
// ("DENIED ..."), or at maxclients ("ERR max number of clients reached"). It
// writes the reason and hangs up. go-redis reads that line as the reply to
// its HELLO, takes it for a server without HELLO, and goes on to its next
// command, which fails with EOF or a broken pipe. The user then reads "EOF"
// for a server that said exactly what was wrong.
//
// greeting wraps each new connection, keeps an error line that arrives as the
// connection's first bytes, and puts it back in place of the network error
// that follows. A command that succeeds forgets it.
type greeting struct {
	refusal atomic.Pointer[string]
}

// RefusedError is Redis's own reason for refusing a connection, with the
// network error go-redis reported in its place.
type RefusedError struct {
	Reply string
	Err   error
}

func (e *RefusedError) Error() string { return e.Reply }
func (e *RefusedError) Unwrap() error { return e.Err }

func (g *greeting) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := next(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &greetingConn{Conn: c, g: g}, nil
	}
}

func (g *greeting) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := g.explain(next(ctx, cmd))
		if err != nil {
			cmd.SetErr(err)
		}
		return err
	}
}

func (g *greeting) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		err := next(ctx, cmds)
		explained := g.explain(err)
		if explained != err {
			for _, cmd := range cmds {
				if cmd.Err() != nil {
					cmd.SetErr(explained)
				}
			}
		}
		return explained
	}
}

// explain swaps a network error for the refusal that caused it.
func (g *greeting) explain(err error) error {
	if err == nil {
		g.refusal.Store(nil)
		return nil
	}
	if !isHangUp(err) {
		return err
	}
	if r := g.refusal.Load(); r != nil {
		return &RefusedError{Reply: *r, Err: err}
	}
	return err
}

// isHangUp reports whether err is the server closing the connection.
func isHangUp(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE)
}

// maxRefusal bounds the error line kept from a connection's first read.
const maxRefusal = 1024

type greetingConn struct {
	net.Conn
	g    *greeting
	read bool
}

func (c *greetingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if !c.read && n > 0 {
		c.read = true
		if p[0] == '-' {
			line := p[1:n]
			if i := bytes.IndexByte(line, '\r'); i >= 0 {
				line = line[:i]
			}
			if len(line) > maxRefusal {
				line = line[:maxRefusal]
			}
			// An old Redis answers HELLO itself with "unknown command";
			// that is not a refusal, and must not explain a later failure.
			if !bytes.HasPrefix(line, []byte("ERR unknown")) {
				r := string(line)
				c.g.refusal.Store(&r)
			}
		}
	}
	return n, err
}

// SyscallConn keeps go-redis's pool health check, which needs the raw socket.
func (c *greetingConn) SyscallConn() (syscall.RawConn, error) {
	sc, ok := c.Conn.(syscall.Conn)
	if !ok {
		return nil, errors.ErrUnsupported
	}
	return sc.SyscallConn()
}
