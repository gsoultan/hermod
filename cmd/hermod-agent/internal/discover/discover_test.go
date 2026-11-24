// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package discover

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// responder answers mDNS queries for _hermod._tcp the way an ESP32 announcing
// itself does, unicast to the querier's port as RFC 6762 asks for a query
// that does not come from port 5353.
func responder(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	pc, err := lc.ListenPacket(t.Context(), "udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var p dnsmessage.Parser
			h, err := p.Start(buf[:n])
			if err != nil {
				continue
			}
			q, err := p.Question()
			if err != nil || q.Name.String() != "_hermod._tcp.local." {
				continue
			}
			svc := dnsmessage.MustNewName("_hermod._tcp.local.")
			inst := dnsmessage.MustNewName("scale-1._hermod._tcp.local.")
			host := dnsmessage.MustNewName("esp32-scale.local.")
			b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true})
			_ = b.StartAnswers()
			_ = b.PTRResource(dnsmessage.ResourceHeader{Name: svc, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.PTRResource{PTR: inst})
			_ = b.StartAdditionals()
			_ = b.SRVResource(dnsmessage.ResourceHeader{Name: inst, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.SRVResource{Target: host, Port: 9100})
			_ = b.AResource(dnsmessage.ResourceHeader{Name: host, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.AResource{A: [4]byte{192, 168, 1, 40}})
			out, _ := b.Finish()
			_, _ = pc.WriteTo(out, from)
		}
	}()
	return pc.LocalAddr().String()
}

func TestBrowseFindsAnAnnouncedDevice(t *testing.T) {
	to := responder(t)
	found, err := Browse(t.Context(), to, []string{"_hermod._tcp"}, 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("found = %+v", found)
	}
	f := found[0]
	if f.Instance != "scale-1" || f.Service != "_hermod._tcp" || f.Host != "esp32-scale.local" ||
		f.Addr != netip.MustParseAddrPort("192.168.1.40:9100") {
		t.Fatalf("found = %+v", f)
	}
}

func TestBrowseWithNoAnswerFindsNothing(t *testing.T) {
	var lc net.ListenConfig
	silent, err := lc.ListenPacket(t.Context(), "udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	found, err := Browse(t.Context(), silent.LocalAddr().String(), []string{"_modbus._tcp"}, 100*time.Millisecond)
	if err != nil || len(found) != 0 {
		t.Fatalf("Browse = %+v, %v", found, err)
	}
}

// The scan finds what listens on the port and nothing else.
func TestScanFindsOpenPorts(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	open, err := Scan(t.Context(), []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("127.0.0.2/31")}, port, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0] != netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port) {
		t.Fatalf("open = %v", open)
	}
}

func TestScanRefusesTooLargeARange(t *testing.T) {
	if _, err := Scan(context.Background(), []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, 502, time.Millisecond); err == nil {
		t.Fatal("a /8 scan was accepted")
	}
	if _, err := ParsePrefixes("192.168.1.0/24, 10.1.2.3"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePrefixes("2001:db8::/120"); err == nil {
		t.Fatal("an IPv6 range was accepted")
	}
}
