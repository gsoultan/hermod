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
	"net"
	"net/netip"

	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/framing"
)

// AllowList is the senders a UDP device keeps datagrams from. Nil keeps all.
type AllowList []netip.Prefix

// ParseAllowList reads addresses and CIDR ranges.
func ParseAllowList(entries []string) (AllowList, error) {
	var out AllowList
	for _, e := range entries {
		if p, err := netip.ParsePrefix(e); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(e)
		if err != nil {
			return nil, fmt.Errorf("allow_from %q: not an address or CIDR range", e)
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}

func (l AllowList) allows(a netip.Addr) bool {
	if len(l) == 0 {
		return true
	}
	a = a.Unmap()
	for _, p := range l {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// RunUDP records each datagram pc receives, from a sender allow keeps, until
// ctx ends. A datagram is a record as it is: framing does not apply, except
// its record size limit. The record's port is the sender. pc is closed on
// return.
func RunUDP(ctx context.Context, pc net.PacketConn, allow AllowList, cfg Config) {
	stop := context.AfterFunc(ctx, func() { _ = pc.Close() })
	defer stop()
	defer func() { _ = pc.Close() }()
	limit := framing.DefaultMaxRecordBytes
	if n := int(cfg.Framing.GetMaxRecordBytes()); n > 0 {
		limit = n
	}
	cfg.status(StatusReading, nil)
	buf := make([]byte, 64<<10)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				cfg.status(StatusDisconnected, err)
			}
			return
		}
		ua, ok := from.(*net.UDPAddr)
		if !ok || !allow.allows(ua.AddrPort().Addr()) {
			continue
		}
		if n > limit {
			if cfg.Oversize != nil {
				cfg.Oversize.Add(1)
			}
			continue
		}
		emitAll(&cfg, [][]byte{append([]byte(nil), buf[:n]...)}, ua.AddrPort().String())
	}
}
