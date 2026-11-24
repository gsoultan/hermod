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
	"net"
	"net/netip"
	"sync/atomic"
	"testing"

	pb "github.com/gsoultan/hermod/pkg/agent/proto"
)

func listenUDP(t *testing.T) net.PacketConn {
	t.Helper()
	var lc net.ListenConfig
	pc, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return pc
}

func sendUDP(t *testing.T, to net.Addr, msgs ...string) {
	t.Helper()
	var d net.Dialer
	c, err := d.DialContext(t.Context(), "udp", to.String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, m := range msgs {
		if _, err := c.Write([]byte(m)); err != nil {
			t.Fatal(err)
		}
	}
}

// Each datagram is one record, newlines and all, tagged with its sender.
func TestRunUDPRecordsEachDatagram(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pc := listenUDP(t)
	c := newCollected()
	done := make(chan struct{})
	go func() { RunUDP(ctx, pc, nil, Config{Emit: c.emit}); close(done) }()
	sendUDP(t, pc.LocalAddr(), "t=21.5\nh=40", "t=21.6")
	got := c.wait(t, 2)
	if got[0] != "t=21.5\nh=40" || got[1] != "t=21.6" {
		t.Fatalf("frames = %q", got)
	}
	c.mu.Lock()
	port := c.ports[0]
	c.mu.Unlock()
	if ap, err := netip.ParseAddrPort(port); err != nil || !ap.Addr().IsLoopback() {
		t.Fatalf("port = %q, want the sender's address", port)
	}
	cancel()
	<-done
}

func TestRunUDPDropsDatagramsFromOtherSenders(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pc := listenUDP(t)
	c := newCollected()
	allow, err := ParseAllowList([]string{"10.0.0.0/8", "192.168.1.7"})
	if err != nil {
		t.Fatal(err)
	}
	go RunUDP(ctx, pc, allow, Config{Emit: c.emit})
	sendUDP(t, pc.LocalAddr(), "from loopback")
	allowLoop, _ := ParseAllowList([]string{"127.0.0.1"})
	pc2 := listenUDP(t)
	go RunUDP(ctx, pc2, allowLoop, Config{Emit: c.emit})
	sendUDP(t, pc2.LocalAddr(), "kept")
	if got := c.wait(t, 1); len(got) != 1 || got[0] != "kept" {
		t.Fatalf("frames = %q", got)
	}
	if _, err := ParseAllowList([]string{"not an address"}); err == nil {
		t.Fatal("a bad allow entry was accepted")
	}
}

func TestRunUDPCountsDatagramsOverTheLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pc := listenUDP(t)
	c := newCollected()
	var over atomic.Uint64
	go RunUDP(ctx, pc, nil, Config{Emit: c.emit, Oversize: &over, Framing: &pb.Framing{MaxRecordBytes: 4}})
	sendUDP(t, pc.LocalAddr(), "too long", "ok")
	if got := c.wait(t, 1); got[0] != "ok" {
		t.Fatalf("frames = %q", got)
	}
	if over.Load() != 1 {
		t.Fatalf("oversize = %d", over.Load())
	}
}
