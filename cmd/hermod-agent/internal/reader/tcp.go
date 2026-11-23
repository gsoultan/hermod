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
	"time"

	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/framing"
)

// TCPClientOpener connects to a LAN device at address.
func TCPClientOpener(address string) Opener {
	return func(ctx context.Context) (io.ReadCloser, string, error) {
		d := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", address)
		if err != nil {
			return nil, "", err
		}
		return conn, address, nil
	}
}

// RunListener accepts connections on ln until ctx ends, framing each one
// separately so two devices writing at once never mix their bytes. A
// connection's port is the address it came from. ln is closed on return.
func RunListener(ctx context.Context, ln net.Listener, cfg Config) {
	var wg sync.WaitGroup
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	defer wg.Wait()
	cfg.status(StatusReading, nil)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				cfg.status(StatusDisconnected, err)
			}
			return
		}
		fr, err := framing.New(cfg.Framing)
		if err != nil {
			_ = conn.Close()
			cfg.status(StatusDisconnected, err)
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = readConn(ctx, conn, conn.RemoteAddr().String(), fr, &cfg)
		}()
	}
}
